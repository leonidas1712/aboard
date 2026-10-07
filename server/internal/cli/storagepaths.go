package cli

import (
	"net/url"
	"path/filepath"
)

func firstStorageURL(flag, env string) string {
	if flag != "" {
		return flag
	}
	return env
}

func storagePath(raw, kind, fallback string) (string, error) {
	if raw == "" {
		return fallback, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != kind || u.Host != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !filepath.IsAbs(u.Path) {
		return "", newError("storage_unsupported", "This build needs a local "+kind+" storage URL without credentials.", "Use "+kind+":///absolute/path.")
	}
	return filepath.Clean(u.Path), nil
}
