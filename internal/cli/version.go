package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Version is set at build time: -ldflags "-X winslot/internal/cli.Version=..."
var Version = "dev"

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the winslot version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintln(cmd.OutOrStdout(), Version)
		},
	}
}
