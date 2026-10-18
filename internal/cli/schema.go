package cli

import (
	"encoding/json"

	"github.com/mik3dev/fal-cli/internal/output"
	"github.com/mik3dev/fal-cli/internal/schema"
	"github.com/spf13/cobra"
)

func newModelsSchemaCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "schema <endpoint>",
		Short: "Show the input and output schema of an endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, paths, err := newAPIClient(cmd, deps)
			if err != nil {
				return err
			}
			raw, _ := cmd.Flags().GetBool("raw")
			refresh, _ := cmd.Flags().GetBool("refresh")
			d, err := schema.Fetch(cmd.Context(), c, paths.Cache, args[0], refresh, deps.Now())
			if err != nil {
				return err
			}
			if raw {
				return output.Success(deps.Stdout, map[string]any{"model": args[0], "openapi": json.RawMessage(d.Raw)})
			}
			s, err := schema.Summarize(d)
			if err != nil {
				return err
			}
			return output.Success(deps.Stdout, map[string]any{"model": args[0], "category": d.Category, "required": s.Required, "fields": s.Fields, "output": s.Output})
		},
	}
	cmd.Flags().Bool("raw", false, "print the full OpenAPI document")
	cmd.Flags().Bool("refresh", false, "bypass the cache")
	return cmd
}
