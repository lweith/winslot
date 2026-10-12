package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"winslot/internal/console"
	"winslot/internal/hostinit"
)

func newHostCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "host",
		Short: "Set up this host's winslot trust material",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "init",
		Short: "Create the host CA, management key and published CA trust line (never replaces existing keys)",
		Long: `Creates, if absent: the SSH host CA that signs VM host keys, the management key
that installs certificates (authorized on VMs only from this host's VM-network
address), and known_hosts.ca, the one line admins add to their known_hosts.
Existing keys are kept. Run by the package on install; safe to run again, e.g.
after changing network.gateway.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			host, err := os.Hostname()
			if err != nil {
				return err
			}
			return hostinit.Init(cfg, host, time.Now(), cmd.OutOrStdout())
		},
	})
	var newPassword bool
	consoleCmd := &cobra.Command{
		Use:   "console",
		Short: "Show the break-glass root password and SSH host keys on the console login prompt",
		Long: `Writes /etc/issue.d/40-winslot.issue, which agetty shows above every local
console login prompt (screen, IPMI, serial): root's password, this host's SSH
host key fingerprints and the VM host CA fingerprint.

The first run sets root to a random password and keeps it in
/etc/winslot/console-root.toml (root-only). Later runs keep it; a password
changed by hand is reported as unknown rather than shown wrong. Run at every
boot by winslot-console.service, so the prompt follows the live host keys.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			res, err := console.Update(console.DefaultPaths(cfg.Storage.Etc), newPassword, time.Now(), chpasswdRoot)
			if err != nil {
				return err
			}
			// Running gettys redraw their prompt; harmless where there are none.
			exec.Command("agetty", "--reload").Run()
			out := cmd.OutOrStdout()
			if res.Generated {
				fmt.Fprintln(out, "root: new random password set")
			}
			if !res.Known {
				fmt.Fprintln(out, "root: password was changed by hand; the console says it is not known")
			}
			fmt.Fprintf(out, "console prompt updated (%d SSH host keys)\n", res.Keys)
			return nil
		},
	}
	consoleCmd.Flags().BoolVar(&newPassword, "new-password", false, "set and show a new random root password")
	cmd.AddCommand(consoleCmd)
	return cmd
}

// chpasswdRoot sets root's password, passing it on stdin (never argv).
func chpasswdRoot(pw string) error {
	c := exec.Command("chpasswd")
	c.Stdin = strings.NewReader("root:" + pw + "\n")
	if out, err := c.CombinedOutput(); err != nil {
		return fmt.Errorf("chpasswd: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
