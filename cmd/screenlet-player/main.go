// Command screenlet-player is the Screenlet Player binary: a lightweight
// digital signage client for Linux that pairs with a Screenlet Studio
// instance, syncs its assigned channel, and plays it back fullscreen.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/BirdRa1n/screenlet-player/internal/api"
	"github.com/BirdRa1n/screenlet-player/internal/device"
	"github.com/BirdRa1n/screenlet-player/internal/media"
	"github.com/BirdRa1n/screenlet-player/internal/playback"
	"github.com/BirdRa1n/screenlet-player/internal/sync"
	"github.com/BirdRa1n/screenlet-player/internal/telemetry"
	"github.com/BirdRa1n/screenlet-player/pkg/version"
)

const (
	syncInterval      = 15 * time.Second
	heartbeatInterval = 20 * time.Second
)

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	reset := flag.Bool("reset", false, "wipe all local pairing state (device identity, API token, channel) so this device can be claimed by a different Screenlet Studio instance — requires local/SSH access, exits without starting the player")
	addr := flag.String("addr", ":8089", "address for the local control API")
	studioURL := flag.String("studio-url", "", "Screenlet Studio base URL, e.g. http://192.168.1.10:7095 (enables pairing + sync; not required if the device will be claimed via network scan instead)")
	mpvBin := flag.String("mpv-bin", "mpv", "mpv executable to use for playback")
	mpvArgs := flag.String("mpv-args", "", "extra space-separated arguments appended to the mpv invocation, e.g. \"--vo=drm\" on a Raspberry Pi")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return
	}

	if *reset {
		if err := device.Reset(); err != nil {
			log.Fatalf("screenlet-player: reset failed: %v", err)
		}
		if err := media.Clear(); err != nil {
			log.Printf("screenlet-player: failed to clear media cache during reset: %v", err)
		}
		fmt.Println("screenlet-player: local state wiped. This device is unclaimed again — it will generate a new identity and pairing code on next start.")
		return
	}

	id, err := device.LoadOrCreate()
	if err != nil {
		log.Fatalf("screenlet-player: failed to load device identity: %v", err)
	}
	log.Printf("screenlet-player %s starting — device %s (%s)", version.Version, id.ID, id.Hostname)

	token, err := device.APIToken()
	if err != nil {
		log.Fatalf("screenlet-player: failed to load API token: %v", err)
	}
	if code, err := device.PairingCode(); err == nil {
		log.Printf("pairing code: %s — shown in Screenlet Studio's Dispositivos panel if discovered via heartbeat", code)
	}

	player := newPlayer(*mpvBin, *mpvArgs)
	defer player.Close()

	// The media cache is what makes playback survive a reboot with no server.
	// If it can't be created we degrade to live streaming rather than fail.
	cache, err := media.NewCache()
	if err != nil {
		log.Printf("media cache unavailable (%v) — offline playback disabled, will stream live", err)
		cache = nil
	}
	code, _ := device.PairingCode()
	screen := newContent(player, cache, code)
	screen.boot()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	studio := &integration{root: ctx, run: func(ctx context.Context, url string) {
		runStudioIntegration(ctx, url, id, player, screen)
	}}

	apiSrv := api.New(player, api.Options{
		Info:  api.Info{DeviceID: id.ID, Hostname: id.Hostname, PlayerVersion: version.Version},
		Token: token,
		Mint: func(studioURL string) (api.ClaimResult, error) {
			newToken, err := device.GenerateAPIToken(studioURL)
			if err != nil {
				return api.ClaimResult{}, err
			}
			code, err := device.PairingCode()
			if err != nil {
				return api.ClaimResult{}, err
			}
			return api.ClaimResult{Token: newToken, DeviceID: id.ID, PairingCode: code}, nil
		},
		OnClaimed: func(_ string, studioURL string) {
			log.Println("device claimed by Screenlet Studio")
			studio.start(studioURL)
		},
		OnStudioURL: func(studioURL string) error {
			if studioURL == studio.current() {
				return nil
			}
			if err := device.SetStudioURL(studioURL); err != nil {
				return err
			}
			studio.start(studioURL)
			return nil
		},
	})

	go func() {
		log.Printf("control API listening on %s", *addr)
		if err := http.ListenAndServe(*addr, apiSrv.Handler()); err != nil {
			log.Fatalf("control API: %v", err)
		}
	}()

	switch {
	case *studioURL != "":
		studio.start(*studioURL)
	case token != "":
		persistedURL, err := device.StudioURL()
		if err != nil {
			log.Printf("failed to load persisted Studio URL: %v", err)
		}
		studio.start(persistedURL)
	default:
		log.Println("not claimed yet — waiting to be discovered and claimed by Screenlet Studio (or pass -studio-url)")
	}

	<-ctx.Done()
	log.Println("shutting down")
}

// newPlayer tries to start a real mpv-backed playback backend. mpv being
// unavailable (not installed, no usable video output) isn't fatal — the
// control API and pairing/sync still work without it, just rendering
// nothing, which is the expected state on a CI runner or a developer's
// Mac with no mpv installed.
func newPlayer(bin, extraArgs string) playback.Player {
	opts := playback.MPVOptions{BinPath: bin}
	if extraArgs != "" {
		opts.ExtraArgs = strings.Fields(extraArgs)
	}

	player, err := playback.NewMPVPlayer(opts)
	if err != nil {
		log.Printf("mpv playback unavailable (%v) — falling back to a no-op player", err)
		return playback.NewNoopPlayer()
	}
	log.Println("mpv playback backend ready")
	return player
}

// runStudioIntegration keeps this device paired and in sync with a
// Screenlet Studio instance until ctx ends (shutdown, or Studio reported a new
// address): heartbeats report health (and, before this device is claimed, are
// how Studio's heartbeat-based discovery learns it exists) and the poller
// picks up channel changes without ever needing a restart. Losing the PC only
// makes these calls fail and retry — what is on screen keeps playing from the
// local cache.
func runStudioIntegration(ctx context.Context, studioURL string, id *device.Identity, player playback.Player, screen *content) {
	log.Printf("syncing with Screenlet Studio at %s", studioURL)
	code, err := device.PairingCode()
	if err != nil {
		log.Printf("pairing: failed to load pairing code for telemetry: %v", err)
	}

	poller := sync.NewPoller(sync.NewClient(studioURL, id.ID))
	if err := poller.Start(syncInterval, func(assignment sync.ChannelAssignment) bool {
		return screen.apply(ctx, assignment)
	}); err != nil {
		log.Printf("sync: failed to start: %v", err)
	}
	defer poller.Stop()

	reporter := telemetry.NewHTTPReporter(studioURL)
	if err := reporter.Start(heartbeatInterval, func() telemetry.Heartbeat {
		status, _ := player.Status()
		return telemetry.Heartbeat{
			DeviceID:      id.ID,
			Hostname:      id.Hostname,
			PairingCode:   code,
			PlayerVersion: version.Version,
			Playing:       status.Playing,
			Source:        status.Source,
			SentAt:        time.Now().UTC(),
		}
	}); err != nil {
		log.Printf("telemetry: failed to start: %v", err)
	}
	defer reporter.Stop()

	<-ctx.Done()
}
