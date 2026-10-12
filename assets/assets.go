// Package assets embeds the files winslot puts into images and VMs: the
// Windows answer files and in-guest scripts (answer/), and the Packer template
// with the pinned payload list (packer/). Embedding keeps the installed binary
// and the files it relies on from drifting apart.
package assets

import "embed"

// FS holds answer/ and packer/.
//
//go:embed answer packer
var FS embed.FS

// WINCFG lists the answer/ files that go onto the WINCFG CD of an image build.
var WINCFG = []string{
	"autounattend.xml",
	"provision.ps1",
	"desktop.ps1",
	"SetupComplete.cmd",
	"firstboot.ps1",
	"wallpaper.jpg",
}
