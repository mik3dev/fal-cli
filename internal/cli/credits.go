package cli

import (
	"github.com/mik3dev/fal-cli/internal/catalog"
	"github.com/mik3dev/fal-cli/internal/output"
	"github.com/spf13/cobra"
)

func newCreditsCmd(deps Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "credits",
		Short: "Show the account balance (needs a key with the BILLING permission)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := newAPIClient(cmd, deps)
			if err != nil {
				return err
			}
			bal, cur, err := catalog.Credits(cmd.Context(), c)
			if err != nil {
				return err
			}
			return output.Success(deps.Stdout, map[string]any{"balance": bal, "currency": cur})
		},
	}
}
