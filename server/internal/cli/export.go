package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

const exportUsage = "aboard export [--board NAME] [--zip] [--out PATH] [--as AGENT] [--json]"

func runExport(ctx context.Context, a *app, args []string) error {
	f := a.flags("export")
	b := f.String("board", "", "the board to export")
	as := f.String("as", "", "the agent to act as")
	zip := f.Bool("zip", false, "include the latest file bytes in a zip archive")
	out := f.String("out", "", "the local destination file")
	if _, err := a.parse(f, args, exportUsage, 0, 0); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	t, c, err := a.taskClient(ctx, *b, *as, "")
	if err != nil {
		return err
	}
	format := "md"
	if *zip {
		format = "zip"
	}
	dest := *out
	if dest == "-" {
		return usageError("Name a local destination file.", exportUsage)
	}
	if dest == "" {
		dest = t.board + "-" + time.Now().UTC().Format("2006-01-02") + "." + format
	}
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(a.env.Dir, dest)
	}
	dest = filepath.Clean(dest)
	if _, err = os.Lstat(dest); err == nil {
		return exportExists(dest)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	value := api.ExportBoardParamsFormat(format)
	response, err := c.api.ExportBoard(ctx, t.board, &api.ExportBoardParams{Format: &value})
	if err != nil {
		return c.unreachable(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		body, err := io.ReadAll(response.Body)
		if err != nil {
			return err
		}
		return apiError(response.StatusCode, body)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".aboard-export-*")
	if err != nil {
		return err
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	n, err := io.Copy(tmp, response.Body)
	if err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// Link publishes a complete download without replacing a concurrently created file.
	if err = os.Link(tmp.Name(), dest); errors.Is(err, fs.ErrExist) {
		return exportExists(dest)
	} else if err != nil {
		return err
	}
	a.emit(struct {
		Path   string `json:"path"`
		Format string `json:"format"`
		Bytes  int64  `json:"bytes"`
	}{dest, format, n}, fmt.Sprintf("Exported %s on %s to %s (%d bytes).\n", t.board, t.server.URL, dest, n))
	return nil
}

func exportExists(dest string) error {
	return newError("output_exists", "The export destination already exists: "+dest, "Choose a different --out path, or move the existing file first.")
}
