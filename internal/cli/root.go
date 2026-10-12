// Package cli defines winslot's command line (cobra).
package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"winslot/internal/config"
	"winslot/internal/disk"
	"winslot/internal/image"
	"winslot/internal/run"
	"winslot/internal/virt"
	"winslot/internal/vm"
)

// Execute runs the command line and returns the process exit code.
func Execute() int {
	if err := NewRoot().ExecuteContext(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "winslot:", err)
		return 1
	}
	return 0
}

// app builds what commands need, lazily: `winslot --help` and shell
// completion work without root, a config file or libvirt.
type app struct {
	configPath string
	conn       *virt.Conn
}

func (a *app) config() (config.Config, error) {
	return config.Load(a.configPath)
}

func (a *app) virt() (*virt.Conn, error) {
	if a.conn == nil {
		c, err := virt.Connect()
		if err != nil {
			return nil, err
		}
		a.conn = c
	}
	return a.conn, nil
}

func (a *app) manager(out io.Writer) (*vm.Manager, error) {
	cfg, err := a.config()
	if err != nil {
		return nil, err
	}
	v, err := a.virt()
	if err != nil {
		return nil, err
	}
	feature, err := virtFeature("/proc/cpuinfo")
	if err != nil {
		return nil, err
	}
	return &vm.Manager{
		Cfg: cfg, Virt: v, Disks: disk.Tools{R: run.Exec{}}, Certifier: vm.NetCertifier{},
		Out: out, VirtFeature: feature, Version: Version,
	}, nil
}

func (a *app) builder(out io.Writer) (image.Builder, error) {
	cfg, err := a.config()
	if err != nil {
		return image.Builder{}, err
	}
	return image.Builder{Cfg: cfg, R: run.Exec{}, Disk: disk.Tools{R: run.Exec{}}, Out: out}, nil
}

// slotNames completes slot names from libvirt (silently nothing when libvirt
// is unreachable, e.g. completion as a non-root user).
func (a *app) slotNames(cmd *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	m, err := a.manager(io.Discard)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	entries, err := m.List()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name+"\t"+e.State)
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}

// virtFeature reads the host CPU's hardware virtualization flag.
func virtFeature(cpuinfo string) (string, error) {
	f, err := os.Open(cpuinfo)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return parseVirtFeature(f)
}

func parseVirtFeature(r io.Reader) (string, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok || strings.TrimSpace(k) != "flags" {
			continue
		}
		for _, f := range strings.Fields(v) {
			if f == "vmx" || f == "svm" {
				return f, nil
			}
		}
		break
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", errors.New("host CPU has no hardware virtualization (vmx/svm)")
}

// NewRoot builds the command tree.
func NewRoot() *cobra.Command {
	a := &app{}
	root := &cobra.Command{
		Use:   "winslot",
		Short: "Licensed Windows VM slots on libvirt",
		Long: `winslot runs Windows VMs as "license slots": libvirt domains whose virtual
hardware identity (SMBIOS UUID, MAC, disk serial/WWN, machine type) never
changes. A slot's VM can be decommissioned and recreated on a fresh disk from a
golden image, and Windows reactivates by itself because the hardware it sees is
the same. See /usr/share/doc/winslot/README.md.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPostRun: func(*cobra.Command, []string) {
			if a.conn != nil {
				a.conn.Close()
			}
		},
	}
	root.PersistentFlags().StringVar(&a.configPath, "config", config.DefaultPath, "configuration file")
	root.AddCommand(newVersionCmd(), newVMCmd(a), newImageCmd(a), newPayloadCmd(a), newHostCmd(a), newSSHCmd(a), newLicenseCmd(a))
	return root
}
