package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newSSHCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh",
		Short: "What admins need to reach the VMs with verified host keys",
	}
	var perVM bool
	knownHosts := &cobra.Command{
		Use:   "known-hosts",
		Short: "Print the known_hosts line(s) for the VMs",
		Long: `Prints the one @cert-authority line that makes an SSH client trust every VM's
host certificate (current and future VMs). Append it to ~/.ssh/known_hosts, or
to a separate file named in the ssh config (see: winslot ssh config).

With --per-vm, also prints each VM's individual host keys as recorded from its
serial port at first boot (for clients that can't use host certificates).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			files := []string{"known_hosts.ca"}
			if perVM {
				files = append(files, "known_hosts")
			}
			for _, f := range files {
				b, err := os.ReadFile(filepath.Join(cfg.Storage.Etc, f))
				if err != nil {
					return fmt.Errorf("%w (run: winslot host init)", err)
				}
				cmd.OutOrStdout().Write(b)
			}
			return nil
		},
	}
	knownHosts.Flags().BoolVar(&perVM, "per-vm", false, "also print each VM's recorded host keys")

	var jump string
	config := &cobra.Command{
		Use:   "config",
		Short: "Print the ~/.ssh/config block for reaching the VMs through this host",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			if jump == "" {
				if jump, err = os.Hostname(); err != nil {
					return err
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), `# VMs on %[2]s, reached through %[1]s. The client checks each VM's host
# certificate against the CA line from: winslot ssh known-hosts
Host *.%[2]s
  User admin
  ProxyJump %[1]s
  UserKnownHostsFile ~/.ssh/known_hosts.winslot
  StrictHostKeyChecking yes
`, jump, cfg.Network.Zone)
			return nil
		},
	}
	config.Flags().StringVar(&jump, "jump", "", "host admins reach first (default: this host's name)")

	cmd.AddCommand(knownHosts, config)
	return cmd
}
