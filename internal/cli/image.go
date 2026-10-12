package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"winslot/assets"
	"winslot/internal/image"
	"winslot/internal/payload"
)

func newImageCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Build, import and list golden Windows images",
	}
	cmd.AddCommand(newImageBuildCmd(a), newImageImportCmd(a), newImageListCmd(a))
	return cmd
}

func newImageBuildCmd(a *app) *cobra.Command {
	var noImport bool
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build a golden image with Packer (unattended, offline, ~30 min), then import it",
		Long: `Builds a golden Windows image: verifies the pinned payload, stages the WINCFG
CD (answer files, installers, virtio drivers, admin SSH keys), and runs Packer:
Windows Setup, Audit Mode, provision.ps1, Sysprep, power off. The build VM has
no network access. The result is verified offline and imported unless
--no-import is given.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, err := a.builder(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			img, err := b.Build(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "built %s\n", img)
			if noImport {
				return nil
			}
			_, err = b.Import(cmd.Context(), img)
			return err
		},
	}
	cmd.Flags().BoolVar(&noImport, "no-import", false, "leave the built image in place instead of importing it")
	return cmd
}

func newImageImportCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "import IMAGE.qcow2",
		Short: "Verify a built image offline and store it as a golden image",
		Args:  cobra.ExactArgs(1),
		ValidArgsFunction: func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
			return []string{"qcow2"}, cobra.ShellCompDirectiveFilterFileExt
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := a.builder(cmd.OutOrStdout())
			if err != nil {
				return err
			}
			_, err = b.Import(cmd.Context(), args[0])
			return err
		},
	}
}

func newImageListCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List golden images (the newest is used for new VMs and recreates)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			dir := filepath.Join(cfg.Storage.Data, "images")
			matches, _ := filepath.Glob(filepath.Join(dir, "win11-*.qcow2"))
			newest, _, _ := image.Latest(dir)
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "IMAGE\tSIZE ON DISK\t")
			for _, m := range matches {
				name := strings.TrimSuffix(filepath.Base(m), ".qcow2")
				mark := ""
				if name == newest {
					mark = "(newest)"
				}
				st, err := os.Stat(m)
				if err != nil {
					return err
				}
				sz := byteSize(st.Size())
				fmt.Fprintf(w, "%s\t%s\t%s\n", name, sz.String(), mark)
			}
			return w.Flush()
		},
	}
}

func newPayloadCmd(a *app) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "payload",
		Short: "Manage the pinned guest payload (installers, drivers)",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "fetch",
		Short: "Download missing payload files and verify every pinned sha256",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := a.config()
			if err != nil {
				return err
			}
			return fetchPayload(cmd.Context(), cfg.Storage.Cache, cmd)
		},
	})
	return cmd
}

func fetchPayload(ctx context.Context, cache string, cmd *cobra.Command) error {
	f, err := assets.FS.Open("packer/payload.list")
	if err != nil {
		return err
	}
	defer f.Close()
	items, err := payload.ParseList(f)
	if err != nil {
		return err
	}
	fetcher := &payload.Fetcher{Dir: filepath.Join(cache, "payload"), Log: func(format string, args ...any) {
		fmt.Fprintf(cmd.OutOrStdout(), format+"\n", args...)
	}}
	return fetcher.Ensure(ctx, items)
}
