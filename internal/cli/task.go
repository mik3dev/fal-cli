package cli

import (
	"fmt"

	"github.com/mik3dev/fal-cli/internal/config"
	"github.com/mik3dev/fal-cli/internal/ledger"
	"github.com/mik3dev/fal-cli/internal/output"
	"github.com/mik3dev/fal-cli/internal/task"
	"github.com/spf13/cobra"
)

const defaultTaskListLimit = 20

func newTaskCmd(deps Deps) *cobra.Command {
	task := &cobra.Command{Use: "task", Short: "Inspect locally recorded requests", RunE: groupRunE}
	task.AddCommand(newTaskListCmd(deps), newTaskGetCmd(deps), newTaskCancelCmd(deps))
	return task
}

func newTaskListCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent requests from the local ledger (newest first)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			limit, _ := cmd.Flags().GetInt("limit")
			if limit < 1 {
				return &output.Error{Code: output.CodeUsage, Message: fmt.Sprintf("--limit must be >= 1, got %d", limit)}
			}
			paths, err := config.ResolvePaths(deps.Env)
			if err != nil {
				return err
			}
			tasks, err := ledger.List(paths.Ledger, limit, warnWriter(cmd, deps))
			if err != nil {
				return &output.Error{Code: output.CodeIO, Message: err.Error()}
			}
			return output.Success(deps.Stdout, map[string]any{"tasks": tasks})
		},
	}
	cmd.Flags().Int("limit", defaultTaskListLimit, "maximum number of requests to list")
	return cmd
}

// resolveEndpoint finds the endpoint of a request: the ledger first, then --model.
func resolveEndpoint(paths config.Paths, id, modelFlag string) (string, error) {
	if e, ok := ledger.Find(paths.Ledger, id); ok && e.Endpoint != "" {
		return e.Endpoint, nil
	}
	if modelFlag != "" {
		return modelFlag, nil
	}
	return "", &output.Error{Code: output.CodeUsage, Message: fmt.Sprintf("request %s is not in the local ledger; pass --model <endpoint>", id)}
}

func addModelFlag(cmd *cobra.Command) {
	cmd.Flags().String("model", "", "endpoint of the request when it is not in the local ledger")
}

func newTaskGetCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <requestId>",
		Short: "Fetch the current state (and result when completed) of a request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, paths, err := newAPIClient(cmd, deps)
			if err != nil {
				return err
			}
			model, _ := cmd.Flags().GetString("model")
			endpoint, err := resolveEndpoint(paths, args[0], model)
			if err != nil {
				return err
			}
			s, err := task.GetStatus(cmd.Context(), c, endpoint, args[0])
			if err != nil {
				return err
			}
			out := map[string]any{"requestId": args[0], "model": endpoint, "state": s.State}
			if s.State == task.StateInQueue {
				out["queuePosition"] = s.QueuePosition
			}
			if s.State == task.StateCompleted {
				raw, err := task.GetResult(cmd.Context(), c, endpoint, args[0])
				if err != nil {
					return err
				}
				out["result"] = raw
			}
			return output.Success(deps.Stdout, out)
		},
	}
	addModelFlag(cmd)
	return cmd
}

func newTaskCancelCmd(deps Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel <requestId>",
		Short: "Cancel a queued request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, paths, err := newAPIClient(cmd, deps)
			if err != nil {
				return err
			}
			model, _ := cmd.Flags().GetString("model")
			endpoint, err := resolveEndpoint(paths, args[0], model)
			if err != nil {
				return err
			}
			status, err := task.Cancel(cmd.Context(), c, endpoint, args[0])
			if err != nil {
				return err
			}
			if status == "CANCELLATION_REQUESTED" {
				_ = ledger.Append(paths.Ledger, ledger.Entry{RequestID: args[0], Endpoint: endpoint, State: task.LedgerCancelled, CreatedAt: deps.Now()})
			}
			return output.Success(deps.Stdout, map[string]any{"requestId": args[0], "model": endpoint, "status": status})
		},
	}
	addModelFlag(cmd)
	return cmd
}
