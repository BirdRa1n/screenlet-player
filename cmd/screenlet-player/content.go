package main

import (
	"context"
	"log"
	"net"
	"strings"
	stdsync "sync"

	"github.com/BirdRa1n/screenlet-player/internal/media"
	"github.com/BirdRa1n/screenlet-player/internal/playback"
	"github.com/BirdRa1n/screenlet-player/internal/sync"
)

// content decides what is on screen. Its one rule: once the device has
// something cached, the screen never goes back to depending on the network.
//
//   - A new channel version keeps the current content on screen until every
//     file of the new one is downloaded and verified; only then does it swap
//     and drop the old files. A failed or partial update can't take away what
//     is playing.
//   - The legacy live stream (/channel/<id> on the PC) is only used when
//     nothing at all is cached — it dies with the PC, and on a Studio without
//     ffmpeg it never worked in the first place.
//   - With nothing to play, the screen shows a status message instead of
//     falling through to the console.
type content struct {
	player playback.Player
	cache  *media.Cache // nil: cache unavailable, live streaming only

	mu       stdsync.Mutex // serializes apply(): two integrations may overlap during a Studio URL switch
	current  *media.Manifest
	complete bool   // every item of current is cached and on screen
	live     string // live URL on screen, when not playing from cache

	pairingCode string
}

func newContent(player playback.Player, cache *media.Cache, pairingCode string) *content {
	return &content{player: player, cache: cache, pairingCode: pairingCode}
}

// boot plays the last channel straight from the local cache, before any
// network contact — what keeps a screen filled after a reboot when the PC is
// off.
func (c *content) boot() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cache == nil {
		c.idle("Aguardando programação")
		return
	}
	manifest, ok, err := media.LoadManifest()
	if err != nil {
		log.Printf("media: failed to load cached manifest: %v", err)
	}
	if !ok || manifest == nil {
		c.idle("Aguardando programação")
		return
	}
	c.cache.Adopt(manifest) // files cached by older versions, stored by filename
	paths := c.cache.LocalPlaylist(manifest)
	if len(paths) == 0 {
		log.Printf("media: cached manifest for channel %s has no usable local files yet", manifest.ChannelID)
		c.idle("Aguardando programação")
		return
	}
	log.Printf("playing cached channel %s (%d of %d item(s)) from local storage", manifest.ChannelID, len(paths), len(manifest.Items))
	if err := c.playLocal(paths, manifest.Loop); err != nil {
		log.Printf("play cached: %v", err)
		return
	}
	c.current = manifest
	c.complete = len(paths) == len(manifest.Items)
}

// apply reacts to an assignment from Studio. It returns true once the
// assignment is fully applied; false asks the poller to deliver it again on
// the next tick (downloads still missing).
func (c *content) apply(ctx context.Context, a sync.ChannelAssignment) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.cache != nil && a.HasItems() {
		m := a.Manifest()
		paths, err := c.cache.Sync(ctx, &m, c.current)
		if err != nil {
			log.Printf("media: sync incomplete: %v", err)
		}

		switch {
		case c.cache.Complete(&m):
			onScreen := c.live == "" && c.complete && c.current != nil && c.current.Version == m.Version
			if !onScreen {
				log.Printf("channel %s (%s): playing %d cached item(s)", m.ChannelID, m.Version, len(paths))
				if err := c.playLocal(paths, m.Loop); err != nil {
					log.Printf("play: %v", err)
					return false
				}
			}
			c.adopt(&m, true)
			c.cache.GC(&m) // old version is off screen now
			return true

		case len(paths) > 0 && !c.hasLocal():
			// Nothing cached on screen yet: show what has arrived, keep
			// downloading the rest on the next ticks.
			log.Printf("channel %s (%s): playing %d of %d item(s) while the rest downloads", m.ChannelID, m.Version, len(paths), len(m.Items))
			if err := c.playLocal(paths, m.Loop); err != nil {
				log.Printf("play: %v", err)
			}
			c.adopt(&m, false)
			return false

		default:
			if c.hasLocal() {
				log.Printf("channel %s (%s): update incomplete — keeping current content on screen, will retry", m.ChannelID, m.Version)
			} else {
				c.playLive(a.PlaylistURL)
			}
			return false
		}
	}

	// No downloadable items: an empty channel, or a Studio too old for the
	// offline manifest.
	if c.hasLocal() {
		log.Printf("channel %s has no downloadable items — keeping cached content on screen", a.ChannelID)
		return true
	}
	c.playLive(a.PlaylistURL)
	return true
}

func (c *content) adopt(m *media.Manifest, complete bool) {
	if err := media.SaveManifest(m); err != nil {
		log.Printf("media: failed to persist manifest: %v", err)
	}
	c.current = m
	c.complete = complete
	c.live = ""
}

func (c *content) hasLocal() bool {
	return c.cache != nil && c.current != nil && c.live == "" && len(c.cache.LocalPlaylist(c.current)) > 0
}

func (c *content) playLocal(paths []string, loop bool) error {
	if err := c.player.PlayPlaylist(paths, loop); err != nil {
		return err
	}
	_ = c.player.ShowMessage("")
	return nil
}

func (c *content) playLive(url string) {
	if url == "" {
		c.idle("Canal sem vídeos")
		return
	}
	if c.live == url {
		return
	}
	log.Printf("nothing cached — streaming live from %s", url)
	if err := c.player.Play(url); err != nil {
		log.Printf("play: %v", err)
		return
	}
	_ = c.player.ShowMessage("")
	c.live = url
}

// idle shows a status screen: what the device is waiting for and how to find
// it, so an installer looking at a black panel knows it's alive.
func (c *content) idle(status string) {
	lines := []string{"Screenlet Player", status}
	var details []string
	if ips := localIPv4s(); len(ips) > 0 {
		details = append(details, "IP "+strings.Join(ips, ", "))
	}
	if c.pairingCode != "" {
		details = append(details, "código "+c.pairingCode)
	}
	if len(details) > 0 {
		lines = append(lines, strings.Join(details, " · "))
	}
	if err := c.player.ShowMessage(strings.Join(lines, "\n")); err != nil {
		log.Printf("screen message: %v", err)
	}
}

func localIPv4s() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
				out = append(out, ipn.IP.String())
			}
		}
	}
	return out
}

// integration owns the sync/telemetry goroutines for the current Studio URL
// and restarts them when that URL changes (POST /studio from Studio).
type integration struct {
	root context.Context
	run  func(ctx context.Context, url string)

	mu     stdsync.Mutex
	url    string
	cancel context.CancelFunc
}

func (in *integration) start(url string) {
	url = strings.TrimRight(url, "/")
	if url == "" {
		return
	}
	in.mu.Lock()
	defer in.mu.Unlock()
	if url == in.url {
		return
	}
	if in.cancel != nil {
		in.cancel()
		log.Printf("Screenlet Studio moved: %s -> %s", in.url, url)
	}
	ctx, cancel := context.WithCancel(in.root)
	in.url, in.cancel = url, cancel
	go in.run(ctx, url)
}

func (in *integration) current() string {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.url
}
