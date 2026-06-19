// Package updater checks GitHub Releases for newer published builds so the
// player can self-update without a Screenlet-Studio-side trigger.
package updater

// Release describes a published GitHub release of screenlet-player.
type Release struct {
	Version     string
	DownloadURL string
}

// Checker looks up the latest published release on GitHub.
type Checker interface {
	// Latest returns the newest published release.
	Latest() (Release, error)
}

// IsNewer reports whether candidate differs from current. A minimal stub
// for now — see docs/ROADMAP.md for swapping in a proper SemVer comparison
// once the updater is implemented.
func IsNewer(current, candidate string) bool {
	return current != candidate
}
