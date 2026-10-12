package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"winslot/internal/license"
	"winslot/internal/virt"
	"winslot/internal/vm"
)

func newVMCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vm",
		Short: "Create, decommission, recreate and list license slots",
	}
	cmd.AddCommand(newVMCreateCmd(a), newVMRecreateCmd(a), newVMDecommissionCmd(a), newVMDeleteCmd(a), newVMRestoreCmd(a),
		newVMListCmd(a), newVMCertifyCmd(a), newVMPushKeysCmd(a), newVMUpdateCmd(a))
	return cmd
}

func newVMDeleteCmd(a *app) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete NAME",
		Short: "Remove a never-activated slot completely (licensed slots are refused)",
		Long: `Removes a slot whose Windows was never activated: the libvirt domain with its
UEFI variables and vTPM, its disk, host keys and discovery record. A licensed
slot (in the license register, or marked licensed in its metadata) is refused:
its identity is what the activation is bound to. The VM must be shut off.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.slotNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			m, err := a.manager(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			if precious, err := m.Precious(name); err != nil {
				return err
			} else if precious {
				return m.Delete(cmd.Context(), name) // refuses, with the reason
			}
			if !yes {
				fmt.Fprintf(cmd.OutOrStdout(), "This deletes %s and its disk for good. Type the name to confirm: ", name)
				line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if strings.TrimSpace(line) != name {
					return fmt.Errorf("not confirmed")
				}
			}
			return m.Delete(cmd.Context(), name)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}

func newVMRestoreCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "restore FILE",
		Short: "Define a licensed slot from its license record (QR code text or license show output)",
		Long: `Defines a slot with exactly the identity in a license record, without a disk,
and adds the record to this host's register. Then install it with
"winslot vm recreate NAME": Windows sees the same hardware and reactivates.
FILE is the text of the QR code on the slot's sheet, or "winslot license show
NAME" output from the old host; "-" reads standard input.`,
		Example: "  winslot vm restore wiresx.toml && winslot vm recreate wiresx",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var data []byte
			var err error
			if args[0] == "-" {
				data, err = io.ReadAll(cmd.InOrStdin())
			} else {
				data, err = os.ReadFile(args[0])
			}
			if err != nil {
				return err
			}
			rec, err := license.ParseRecord(data)
			if err != nil {
				return err
			}
			m, err := a.manager(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			return m.Restore(cmd.Context(), rec)
		},
	}
}

func newVMUpdateCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "update NAME...",
		Short: "Bring slots' definitions up to the current hardware template (identity kept)",
		Long: `Re-renders each slot's libvirt definition from the current template (video,
title, devices) while carrying its identity (UUID, MAC, disk serial/WWN,
machine type) and sizing over unchanged. libvirt applies it at the slot's next
cold boot: shut the VM down, then start it. recreate does this automatically.`,
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: a.slotNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := a.manager(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			for _, name := range args {
				if err := m.Update(cmd.Context(), name); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func newVMPushKeysCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "push-keys [NAME...]",
		Short: "Install the admin key list (/etc/winslot/authorized_keys) on running VMs",
		Long: `Replaces each VM's administrators_authorized_keys with /etc/winslot/authorized_keys,
the list new golden images are built with. Without names, pushes to every running
slot. The connection is verified with the host CA.`,
		ValidArgsFunction: a.slotNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := a.manager(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			if len(args) == 0 {
				entries, err := m.List()
				if err != nil {
					return err
				}
				for _, e := range entries {
					if e.State == string(virt.Running) {
						args = append(args, e.Name)
					}
				}
			}
			var failed []string
			for _, name := range args {
				if err := m.PushKeys(cmd.Context(), name); err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), err)
					failed = append(failed, name)
				}
			}
			if len(failed) > 0 {
				return fmt.Errorf("key push failed for: %s", strings.Join(failed, ", "))
			}
			return nil
		},
	}
}

func printPassword(cmd *cobra.Command, name, pw string) {
	fmt.Fprintf(cmd.OutOrStdout(), "\n  admin password for %s: %s\n  (shown once, stored nowhere: put it in the password manager)\n\n", name, pw)
}

func newVMCreateCmd(a *app) *cobra.Command {
	disk, mem := byteSize(80<<30), byteSize(8<<30)
	var cpus uint
	var nested bool
	cmd := &cobra.Command{
		Use:   "create NAME",
		Short: "Create a new license slot and its VM from the newest golden image",
		Long: `Creates a license slot: a new, fixed hardware identity (SMBIOS UUID, MAC,
disk serial and WWN, machine type) and a VM using it, on an independent copy of
the newest golden image. NAME becomes the libvirt domain name and the Windows
hostname. The command waits for first boot, records the VM's SSH host keys from
its serial port, installs a CA-signed host certificate, checks the VM is
reachable, and publishes it for Prometheus.`,
		Example: "  winslot vm create wiresx\n  winslot vm create winlink --disk 120G --memory 6G --cpus 2",
		Args:    cobra.ExactArgs(1),
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := a.manager(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			pw, err := m.Create(cmd.Context(), args[0], vm.CreateOptions{
				DiskBytes: uint64(disk), MemoryMiB: uint(uint64(mem) >> 20), VCPUs: cpus, Nested: nested,
			})
			if pw != "" {
				printPassword(cmd, args[0], pw)
			}
			return err
		},
	}
	cmd.Flags().Var(&disk, "disk", "disk size (grows the golden image; Windows extends C: on first boot)")
	cmd.Flags().Var(&mem, "memory", "memory size")
	cmd.Flags().UintVar(&cpus, "cpus", 4, "virtual CPU cores (presented as one socket)")
	cmd.Flags().BoolVar(&nested, "nested", false, "expose hardware virtualization to the guest (WSL2, Docker Desktop, VBS); widens the host's attack surface")
	return cmd
}

func newVMRecreateCmd(a *app) *cobra.Command {
	var disk byteSize
	cmd := &cobra.Command{
		Use:   "recreate NAME",
		Short: "Give a decommissioned slot a fresh disk from the newest golden image",
		Long: `Recreates a decommissioned slot's VM under its unchanged identity, on a fresh
copy of the newest golden image. Windows sees the same hardware, so a license
activated on the slot reactivates by itself.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.slotNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := a.manager(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			pw, err := m.Recreate(cmd.Context(), args[0], uint64(disk))
			if pw != "" {
				printPassword(cmd, args[0], pw)
			}
			return err
		},
	}
	cmd.Flags().Var(&disk, "disk", "new disk size (default: the slot's recorded size)")
	return cmd
}

func newVMDecommissionCmd(a *app) *cobra.Command {
	var yes bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "decommission NAME",
		Short: "Delete a VM's disk, keeping its license slot (identity) for a later recreate",
		Long: `Shuts the VM down, deletes its disk and snapshots, and keeps the libvirt
domain: its identity, UEFI variables and vTPM. The slot stays listed as
decommissioned; "winslot vm recreate NAME" brings it back. Never undefine a slot
with virsh: the identity is the license.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.slotNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			m, err := a.manager(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			if _, err := m.Slot(name); err != nil {
				return err
			}
			if !yes {
				fmt.Fprintf(cmd.OutOrStdout(), "This deletes %s's disk and everything on it. Type the name to confirm: ", name)
				line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if strings.TrimSpace(line) != name {
					return fmt.Errorf("not confirmed")
				}
			}
			if st, err := m.Virt.State(name); err != nil {
				return err
			} else if st == virt.Running {
				fmt.Fprintf(cmd.OutOrStdout(), "== shut down %s (up to %v)\n", name, timeout)
				if err := m.Virt.Shutdown(name); err != nil {
					return err
				}
				deadline := time.Now().Add(timeout)
				for st == virt.Running && time.Now().Before(deadline) {
					time.Sleep(5 * time.Second)
					if st, err = m.Virt.State(name); err != nil {
						return err
					}
				}
				if st != virt.ShutOff {
					return fmt.Errorf("%s did not shut down; shut it down from Windows and try again", name)
				}
			}
			if err := m.Decommission(cmd.Context(), name); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Decommissioned %s. The license slot is kept: winslot vm recreate %s\n", name, name)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	cmd.Flags().DurationVar(&timeout, "shutdown-timeout", 5*time.Minute, "how long to wait for Windows to shut down")
	return cmd
}

func newVMListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List license slots: running, shut off or decommissioned",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := a.manager(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			entries, err := m.List()
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tSTATE\tLICENSE\tGOLDEN IMAGE\tDISK\tNESTED")
			for _, e := range entries {
				d := byteSize(e.Meta.DiskBytes)
				lic := e.License
				if lic == "" {
					lic = "- (disposable)"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%v\n", e.Name, e.State, lic, e.Meta.Golden, d.String(), e.Meta.Nested)
			}
			return w.Flush()
		},
	}
}

func newVMCertifyCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "certify NAME",
		Short: "Record a VM's SSH host keys from its serial port and (re)issue its host certificate",
		Long: `Reads the host keys the VM reported on its serial port at first boot, records
them, signs the ed25519 key with the host CA, installs the certificate over SSH
pinned to that key, and runs the readiness check. Run it after rotating the
host CA.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.slotNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := a.manager(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			if err := m.Certify(cmd.Context(), args[0]); err != nil {
				return err
			}
			return m.PublishSRV(args[0])
		},
	}
}
