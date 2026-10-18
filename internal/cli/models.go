package cli

import (
	"github.com/mik3dev/fal-cli/internal/api"
	"github.com/mik3dev/fal-cli/internal/catalog"
	"github.com/mik3dev/fal-cli/internal/config"
	"github.com/mik3dev/fal-cli/internal/output"
	"github.com/spf13/cobra"
)

// newAPIClient resolves paths, config and key (auth error if missing) and builds a client.
func newAPIClient(cmd *cobra.Command, deps Deps) (*api.Client, config.Paths, error) {
	c, paths, _, err := newAPIClientCfg(cmd, deps)
	return c, paths, err
}

// newAPIClientCfg is newAPIClient that also returns the loaded config.
func newAPIClientCfg(cmd *cobra.Command, deps Deps) (*api.Client, config.Paths, config.Config, error) {
	paths, err := config.ResolvePaths(deps.Env)
	if err != nil {
		return nil, paths, config.Config{}, err
	}
	cfg, err := config.Load(paths, warnWriter(cmd, deps))
	if err != nil {
		return nil, paths, cfg, err
	}
	key, _, err := config.ResolveKey(deps.Env, cfg)
	if err != nil {
		return nil, paths, cfg, err
	}
	c := api.New(key, deps.Env)
	if deps.Transport != nil {
		c.HTTP.Transport = deps.Transport
		c.UploadHTTP.Transport = deps.Transport
	}
	if deps.Sleep != nil {
		c.Sleep = deps.Sleep
	}
	if debug, _ := cmd.Flags().GetBool("debug"); debug {
		c.Debug = deps.Stderr
	}
	return c, paths, cfg, nil
}

func newCatalog(cmd *cobra.Command, deps Deps) (*catalog.Service, error) {
	c, paths, err := newAPIClient(cmd, deps)
	if err != nil {
		return nil, err
	}
	return &catalog.Service{Client: c, CacheDir: paths.Cache, Now: deps.Now}, nil
}

func newModelsCmd(deps Deps) *cobra.Command {
	models := &cobra.Command{Use: "models", Short: "Browse the fal.ai model catalog", RunE: groupRunE}
	models.AddCommand(newModelsListCmd(deps), newModelsSearchCmd(deps), newModelsPriceCmd(deps), newModelsSchemaCmd(deps))
	return models
}

func addFilterFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.String("type", "", "asset type: image, video or audio")
	f.String("category", "", "native fal category (e.g. text-to-video); wins over --type")
	f.Bool("deprecated", false, "include deprecated endpoints")
	f.Int("limit", 50, "maximum entries per page")
	f.String("cursor", "", "continue from nextCursor of a previous page")
	f.Bool("refresh", false, "bypass the cache")
}

func queryFromFlags(cmd *cobra.Command, text string) (catalog.Query, bool) {
	f := cmd.Flags()
	typ, _ := f.GetString("type")
	cat, _ := f.GetString("category")
	dep, _ := f.GetBool("deprecated")
	limit, _ := f.GetInt("limit")
	cursor, _ := f.GetString("cursor")
	refresh, _ := f.GetBool("refresh")
	return catalog.Query{Type: typ, Category: cat, Text: text, Cursor: cursor, Limit: limit, Deprecated: dep}, refresh
}

func emitPage(cmd *cobra.Command, deps Deps, text string) error {
	s, err := newCatalog(cmd, deps)
	if err != nil {
		return err
	}
	q, refresh := queryFromFlags(cmd, text)
	p, err := s.List(cmd.Context(), q, refresh)
	if err != nil {
		return err
	}
	return output.Success(deps.Stdout, map[string]any{"models": p.Models, "nextCursor": nullable(p.NextCursor), "hasMore": p.HasMore})
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func newModelsListCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List catalog endpoints (one page)",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return emitPage(cmd, deps, "") },
	}
	addFilterFlags(cmd)
	return cmd
}

func newModelsSearchCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search <text>",
		Short: "Search the catalog by free text",
		Args:  cobra.ExactArgs(1),
		RunE:  func(cmd *cobra.Command, args []string) error { return emitPage(cmd, deps, args[0]) },
	}
	addFilterFlags(cmd)
	return cmd
}

func newModelsPriceCmd(deps Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "price <endpoint>",
		Short: "Show the unit price of an endpoint",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := newCatalog(cmd, deps)
			if err != nil {
				return err
			}
			p, err := s.Price(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return output.Success(deps.Stdout, map[string]any{"model": args[0], "price": p})
		},
	}
}
