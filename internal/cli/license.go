package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"winslot/internal/license"
	"winslot/internal/winkey"
)

func newLicenseCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "license",
		Short: "The license register: activated slots, their keys and printable sheets",
		Long: `A slot whose Windows is activated is recorded in the license register
(/etc/winslot/licenses.toml, root only) with its key, when it can be known,
and its identity. Recorded slots are precious: they can be decommissioned and
recreated, never deleted. Slots that were never activated are disposable.

"license scan" finds newly activated slots (a systemd timer runs it every six
hours) and writes a one-page sheet per slot to /etc/winslot/sheets/ for the
board's records.`,
	}
	cmd.AddCommand(newLicenseScanCmd(a), newLicenseRecordCmd(a), newLicenseListCmd(a), newLicenseShowCmd(a),
		newLicensePrintCmd(a), newLicenseReleaseCmd(a))
	return cmd
}

func newLicenseScanCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "scan [NAME...]",
		Short: "Ask running slots whether Windows is activated; record newly activated ones",
		Long: `Asks Windows in each running slot (all slots without names) for its
activation state over SSH. A slot activated and not yet registered is recorded,
with its key when it can be read from the VM and confirmed against what Windows
reports, and its sheet is written. Records are never changed or removed by a
scan; discrepancies are reported. Exit status 1 if any slot reported an error
or warning.`,
		ValidArgsFunction: a.slotNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := a.manager(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			host, _ := os.Hostname()
			results, err := m.LicenseScan(cmd.Context(), args, host)
			problems := 0
			for _, r := range results {
				fmt.Fprintf(cmd.OutOrStdout(), "%-15s %-10s %s\n", r.Slot, r.State, r.Detail)
				if r.State == "error" || r.State == "warning" {
					problems++
				}
			}
			if err != nil {
				return err
			}
			if problems > 0 {
				return fmt.Errorf("%d slot(s) need attention", problems)
			}
			return nil
		},
	}
}

func newLicenseRecordCmd(a *app) *cobra.Command {
	var key, purchase string
	var keyStdin bool
	cmd := &cobra.Command{
		Use:   "record NAME",
		Short: "Add a registered slot's product key or purchase details",
		Long: `Sets the product key and/or purchase details of a slot in the register. If the
slot runs, the key must be the one Windows has installed (its last five
characters are checked). A slot activated by digital license has only the
generic key installed, so its key can't be checked and is stored as unchecked.
The slot's sheet is rewritten: print it again.`,
		Example: `  winslot license record wiresx --purchase "Microsoft Store order 1234, treasurer, 2026-10-01"
  winslot license record wiresx --key-stdin`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.slotNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			if keyStdin {
				fmt.Fprint(cmd.ErrOrStderr(), "Product key: ")
				line, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if err != nil && line == "" {
					return err
				}
				key = strings.TrimSpace(line)
			}
			if key == "" && purchase == "" {
				return fmt.Errorf("nothing to record: give --key, --key-stdin or --purchase")
			}
			m, err := a.manager(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			return m.RecordKey(cmd.Context(), args[0], key, purchase)
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "product key (visible in the process list and shell history: prefer --key-stdin)")
	cmd.Flags().BoolVar(&keyStdin, "key-stdin", false, "read the product key from standard input")
	cmd.Flags().StringVar(&purchase, "purchase", "", "where, when and by whom the license was bought")
	cmd.MarkFlagsMutuallyExclusive("key", "key-stdin")
	return cmd
}

func newLicenseListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List registered (activated) slots; keys are shown by their last five characters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			m, err := a.manager(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			reg, err := license.Load(m.RegisterPath())
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "SLOT\tKEY\tKEY SOURCE\tACTIVATED\tUUID\tPURCHASE")
			for _, r := range reg.Licenses {
				k := "-"
				if r.Key != "" {
					k = "…" + winkey.Partial(r.Key)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", r.Slot, k, r.KeySource, r.Activated.Format("2006-01-02"), r.UUID, r.Purchase)
			}
			return w.Flush()
		},
	}
}

func newLicenseShowCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:               "show NAME",
		Short:             "Print a slot's full license record (with the key), as on its sheet's QR code",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.slotNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := a.manager(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			reg, err := license.Load(m.RegisterPath())
			if err != nil {
				return err
			}
			r, ok := reg.Find(args[0])
			if !ok {
				return fmt.Errorf("%s is not in the license register", args[0])
			}
			b, err := r.Marshal()
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(b)
			return err
		},
	}
}

func newLicensePrintCmd(a *app) *cobra.Command {
	var dir string
	cmd := &cobra.Command{
		Use:   "print [NAME...]",
		Short: "Write registered slots' one-page PDF sheets (all without names)",
		Long: `Writes NAME.pdf for each registered slot: the key, purchase details, the slot
identity, how to bring the slot back, and a QR code holding the license record
for "winslot vm restore". The sheets carry product keys: they are written
root-only. Print them for the board and keep them like keys.`,
		ValidArgsFunction: a.slotNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			m, err := a.manager(cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if len(args) == 0 {
				reg, err := license.Load(m.RegisterPath())
				if err != nil {
					return err
				}
				for _, r := range reg.Licenses {
					args = append(args, r.Slot)
				}
				if len(args) == 0 {
					return fmt.Errorf("the license register is empty")
				}
			}
			for _, name := range args {
				path, err := m.WriteSheet(name, dir)
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), path)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&dir, "output", "o", "", "directory to write to (default /etc/winslot/sheets)")
	return cmd
}

func newLicenseReleaseCmd(a *app) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "release NAME",
		Short: "Remove a decommissioned slot from the register (retail transfer of its license)",
		Long: `For moving a retail license to other hardware. The slot must be decommissioned
(no disk) or gone. Its record and sheet are removed and the slot becomes
disposable. Keep the old sheet until the key is activated elsewhere: it is
the only other copy of the key.`,
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.slotNames,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if !yes {
				fmt.Fprintf(cmd.OutOrStdout(), "This removes %s's license record and sheet. Type the name to confirm: ", name)
				line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if strings.TrimSpace(line) != name {
					return fmt.Errorf("not confirmed")
				}
			}
			m, err := a.manager(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			return m.Release(name)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask for confirmation")
	return cmd
}
