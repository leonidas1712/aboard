package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"

	"github.com/leonidas1712/aboard/server/internal/blobs/disk"
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/store/sqlite"
)

const storageUsage = "aboard storage check [--data PATH] [--db URL] [--files URL] | copy --from URL --to URL [--json]"

func runStorage(ctx context.Context, a *app, args []string) error {
	f := a.flags("storage")
	dbURL := f.String("db", "", "the SQLite database URL")
	filesURL := f.String("files", "", "the disk blob store URL")
	data := f.String("data", "", "the server data folder")
	from := f.String("from", "", "the source disk store URL")
	to := f.String("to", "", "the destination disk store URL")
	pos, err := a.parse(f, args, storageUsage, 1, 1)
	if err != nil {
		return err
	}
	if err := a.refuseInSession("Storage maintenance", "aboard storage "+pos[0]); err != nil {
		return err
	}
	switch pos[0] {
	case "check":
		if *from != "" || *to != "" {
			return usageError("Use --from and --to with storage copy.", storageUsage)
		}
		p, err := a.paths()
		if err != nil {
			return err
		}
		dataDir := firstStorageURL(*data, a.env.Getenv("ABOARD_DATA"))
		if dataDir == "" {
			dataDir = p.data
		}
		if !filepath.IsAbs(dataDir) {
			return usageError("Use an absolute data folder.", storageUsage)
		}
		dbPath, err := storagePath(firstStorageURL(*dbURL, a.env.Getenv("ABOARD_DB")), "sqlite", filepath.Join(dataDir, "aboard.db"))
		if err != nil {
			return err
		}
		filesPath, err := storagePath(firstStorageURL(*filesURL, a.env.Getenv("ABOARD_FILES")), "disk", filepath.Join(dataDir, "files"))
		if err != nil {
			return err
		}
		st, err := sqlite.OpenReadOnly(ctx, dbPath)
		if err != nil {
			return err
		}
		defer func() { _ = st.Close() }()
		var versions []board.Blob
		if err := st.Read(ctx, func(tx board.ReadTx) error { var e error; versions, e = tx.BlobVersions(); return e }); err != nil {
			return err
		}
		blobs, err := disk.OpenExisting(filesPath)
		if err != nil {
			return err
		}
		defer func() { _ = blobs.Close() }()
		out := struct {
			DB      string   `json:"db"`
			Files   string   `json:"files"`
			Checked int      `json:"checked"`
			Missing []string `json:"missing"`
			Corrupt []string `json:"corrupt"`
		}{DB: "sqlite", Files: (&url.URL{Scheme: "disk", Path: filesPath}).String(), Missing: []string{}, Corrupt: []string{}}
		for _, v := range versions {
			out.Checked++
			stored, e := blobs.Stat(ctx, v.Digest)
			if errors.Is(e, fs.ErrNotExist) {
				out.Missing = append(out.Missing, v.Digest)
			} else if e != nil || stored.Size != v.Size {
				out.Corrupt = append(out.Corrupt, v.Digest)
			}
		}
		sort.Strings(out.Missing)
		sort.Strings(out.Corrupt)
		text := fmt.Sprintf("Checked %d file versions: %d missing, %d corrupt.\n", out.Checked, len(out.Missing), len(out.Corrupt))
		a.emit(out, text)
		if len(out.Missing)+len(out.Corrupt) > 0 {
			return errCheckFailed
		}
		return nil
	case "copy":
		if *from == "" || *to == "" || *dbURL != "" || *filesURL != "" || *data != "" {
			return usageError("Name --from and --to disk stores.", storageUsage)
		}
		srcPath, err := storagePath(*from, "disk", "")
		if err != nil {
			return err
		}
		dstPath, err := storagePath(*to, "disk", "")
		if err != nil {
			return err
		}
		if _, err := os.Stat(srcPath); err != nil {
			return err
		}
		src, err := disk.OpenExisting(srcPath)
		if err != nil {
			return err
		}
		defer func() { _ = src.Close() }()
		dst, err := disk.Open(dstPath)
		if err != nil {
			return err
		}
		defer func() { _ = dst.Close() }()
		out := struct {
			Copied  int      `json:"copied"`
			Skipped int      `json:"skipped"`
			Failed  []string `json:"failed"`
		}{Failed: []string{}}
		err = src.Walk(ctx, func(blob board.Blob) error {
			stored, e := dst.Stat(ctx, blob.Digest)
			if e == nil && stored.Size == blob.Size {
				out.Skipped++
				return nil
			}
			if e != nil && !errors.Is(e, fs.ErrNotExist) {
				out.Failed = append(out.Failed, blob.Digest)
				return nil
			}
			r, e := src.Open(ctx, blob.Digest)
			if e != nil {
				out.Failed = append(out.Failed, blob.Digest)
				return nil //nolint:nilerr // Collect per-blob failures so the operator sees the complete copy result.
			}
			copied, e := dst.Put(ctx, r, blob.Size)
			closeErr := r.Close()
			if e != nil || closeErr != nil || copied.Digest != blob.Digest {
				out.Failed = append(out.Failed, blob.Digest)
			} else {
				out.Copied++
			}
			return nil
		})
		if err != nil {
			return err
		}
		a.emit(out, fmt.Sprintf("Copied %d blobs; %d already present; %d failed.\n", out.Copied, out.Skipped, len(out.Failed)))
		if len(out.Failed) > 0 {
			return errReportedFailure
		}
		return nil
	default:
		return usageError("Use storage check or copy.", storageUsage)
	}
}
