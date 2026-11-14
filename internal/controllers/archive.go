package controllers

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const archiveManifestName = "manifest.json"

// archiveFile describes one file stored in an export archive.
type archiveFile struct {
	Name  string
	Path  string
	Store bool
}

func writeArchiveJSON(zw *zip.Writer, name string, value any) error {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(0o644)
	writer, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = writer.Write(data)
	return err
}

func writeArchiveFile(zw *zip.Writer, name, path string, store bool) error {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return err
	}
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = name
	if store {
		header.Method = zip.Store
	} else {
		header.Method = zip.Deflate
	}
	writer, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(writer, file)
	return err
}

func readArchiveJSON(zr *zip.Reader, name string, out any) error {
	file, err := zr.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewDecoder(file).Decode(out)
}

// safeArchiveRel validates an archive entry or record path and returns a
// slash-separated relative path that cannot escape its target directory.
func safeArchiveRel(name string) (string, bool) {
	name = strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
	name = strings.TrimLeft(name, "/")
	if name == "" {
		return "", false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(name)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.ContainsRune(clean, 0) {
		return "", false
	}
	return clean, true
}

func saveArchiveEntry(dir, name string, reader io.Reader, mode os.FileMode) (int64, error) {
	rel, ok := safeArchiveRel(name)
	if !ok {
		return 0, os.ErrInvalid
	}
	target := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return 0, err
	}
	written, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil {
		return written, copyErr
	}
	return written, closeErr
}

func archiveEntryName(prefix, rel string) string {
	rel = strings.TrimLeft(strings.ReplaceAll(rel, "\\", "/"), "/")
	return strings.TrimRight(prefix, "/") + "/" + rel
}
