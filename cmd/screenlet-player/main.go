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
	addr := flag.String("addr", ":8089", "address for the local control API")
	studioURL := flag.String("studio-url", "", "Screenlet Studio base URL, e.g. http://192.168.1.10:7095 (enables pairing + sync)")
	mpvBin := flag.String("mpv-bin", "mpv", "mpv executable to use for playback")
	mpvArgs := flag.String("mpv-args", "", "extra space-separated arguments appended to the mpv invocation, e.g. \"--vo=drm\" on a Raspberry Pi")
	flag.Parse()

	if *showVersion {
		fmt.Println(version.String())
		return
	}

	id, err := device.LoadOrCreate()
	if err != nil {
		log.Fatalf("screenlet-player: failed to load device identity: %v", err)
	}
	log.Printf("screenlet-player %s starting — device %s (%s)", version.Version, id.ID, id.Hostname)

	player := newPlayer(*mpvBin, *mpvArgs)
	defer player.Close()
	apiSrv := api.New(player)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("control API listening on %s", *addr)
		if err := http.ListenAndServe(*addr, apiSrv.Handler()); err != nil {
			log.Fatalf("control API: %v", err)
		}
	}()

	if *studioURL == "" {
		log.Println("no -studio-url provided — running standalone (control API only, no pairing/sync)")
	} else {
		go runStudioIntegration(ctx, *studioURL, id, player)
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
// Screenlet Studio instance: heartbeats announce the device (and later
// report its health) so it can be claimed in the Dispositivos panel, and
// the poller picks up channel changes without ever needing a restart.
func runStudioIntegration(ctx context.Context, studioURL string, id *device.Identity, player playback.Player) {
	code, err := device.PairingCode()
	if err != nil {
		log.Printf("pairing: failed to load pairing code: %v", err)
	} else {
		log.Printf("pairing code: %s — open Screenlet Studio > Dispositivos > Screenlet Player to pair this device", code)
	}

	syncClient := sync.NewClient(studioURL, id.ID)
	poller := sync.NewPoller(syncClient)
	if err := poller.Start(syncInterval, func(assignment sync.ChannelAssignment) {
		log.Printf("channel assignment changed: %s -> %s", assignment.ChannelID, assignment.PlaylistURL)
		if err := player.Play(assignment.PlaylistURL); err != nil {
			log.Printf("play: %v", err)
		}
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
