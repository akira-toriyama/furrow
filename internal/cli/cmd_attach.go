package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/akira-toriyama/furrow/internal/core"
	"github.com/spf13/cobra"
)

func newAttachCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "attach <id> <file>",
		Short: "Attach a media file to a task (copies into bodies/assets/, links it from the body)",
		Long: "Copy an image or video into the task's asset area (.furrow/bodies/assets/<id>-*)\n" +
			"and append a relative markdown reference to the task body. Because the body is a\n" +
			"committed .md, the whole attach lands in git from the terminal alone — no web\n" +
			"session, no external upload. Images embed inline (![...]); other media link.\n" +
			"LFS-independent: if .gitattributes tracks the extension, git-lfs handles the blob\n" +
			"transparently.",
		Example: "  furrow attach t-k3m9p ./screenshot.png",
		Args:    cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := openApp()
			if err != nil {
				return err
			}
			id, src := args[0], args[1]
			// #nosec G304 -- src is the operator's own attach argument;
			// reading the file they named is the command's purpose.
			data, err := os.ReadFile(src)
			if err != nil {
				return core.Validationf("", "read %q: %v", src, err)
			}
			res, err := a.Attach(id, filepath.Base(src), data)
			if err != nil {
				return err
			}
			if jsonMode() {
				emitObject(map[string]string{"id": res.ID, "asset": res.Path, "ref": res.Ref, "line": res.Line})
				return nil
			}
			fmt.Fprintf(out, "attached %s  %s\n", res.ID, res.Path)
			return nil
		},
	}
}
