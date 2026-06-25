package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/akira-toriyama/furrow/internal/app"
	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

func newAddCmd() *cobra.Command {
	var (
		status   string
		priority int
		labels   []string
		parent   string
		deps     []string
		refs     []string
		body     string
	)
	cmd := &cobra.Command{
		Use:   "add <title>...",
		Short: "Add a task",
		Long:  "Add a task. The id is assigned automatically (frozen, never reused) and a\nbodies/<id>.md file is created, seeded with the title as a heading.",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			opts := app.AddOpts{
				Status: status, Labels: labels, Parent: parent,
				Deps: deps, Refs: refs, Body: body,
			}
			if cmd.Flags().Changed("priority") {
				p := priority
				opts.Priority = &p
			}
			t, err := a.Add(strings.Join(args, " "), opts)
			if err != nil {
				return err
			}
			if flagJSON {
				printJSON(t)
				return nil
			}
			printOK("added", t)
			return nil
		},
	}
	cmd.Flags().StringVarP(&status, "status", "s", "", "lane (default: config lanes.default)")
	cmd.Flags().IntVarP(&priority, "priority", "p", 0, "explicit priority (default: append in lane)")
	cmd.Flags().StringSliceVarP(&labels, "label", "l", nil, "label (repeatable)")
	cmd.Flags().StringVar(&parent, "parent", "", "parent task id")
	cmd.Flags().StringSliceVar(&deps, "dep", nil, "dependency task id (repeatable)")
	cmd.Flags().StringSliceVar(&refs, "ref", nil, "reference (file:line or URL, repeatable)")
	cmd.Flags().StringVar(&body, "body", "", "initial body markdown (default: a heading from the title)")
	return cmd
}

func newEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit a task's markdown body in $EDITOR",
		Long: "Open bodies/<id>.md in $EDITOR. In a non-interactive context (no TTY) it\n" +
			"prints the absolute body path instead of launching an editor, so an agent\n" +
			"can edit the file directly.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			path, err := a.EditPath(args[0])
			if err != nil {
				return err
			}
			if flagJSON {
				printJSON(map[string]string{"path": path})
				return nil
			}
			// Non-interactive: emit the path; the caller (or Claude) edits it.
			if !isTTY() {
				fmt.Fprintln(out, path)
				return nil
			}
			editor := firstNonEmpty(os.Getenv("FURROW_EDITOR"), os.Getenv("VISUAL"), os.Getenv("EDITOR"), "vi")
			parts := strings.Fields(editor)
			parts = append(parts, path)
			ed := exec.Command(parts[0], parts[1:]...)
			ed.Stdin, ed.Stdout, ed.Stderr = os.Stdin, os.Stdout, os.Stderr
			if err := ed.Run(); err != nil {
				return core.Internalf(args[0], "editor %q failed: %v", editor, err)
			}
			return nil
		},
	}
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
