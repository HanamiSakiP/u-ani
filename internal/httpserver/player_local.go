package httpserver

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/HanamiSakiP/u-ani/internal/config"
	"github.com/HanamiSakiP/u-ani/internal/controllers"
)

type playLocalRequest struct {
	Path string `json:"path"`
}

type playLocalResponse struct {
	Path    string `json:"path"`
	Command string `json:"command"`
}

func adminPlayLocal(w http.ResponseWriter, r *http.Request) {
	var body playLocalRequest
	if err := decodeBody(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	path, err := resolveLocalMedia(body.Path)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	command, err := launchLocalPlayer(path)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, playLocalResponse{Path: path, Command: command})
}

func resolveLocalMedia(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("missing media path")
	}
	if decoded, err := url.PathUnescape(raw); err == nil {
		raw = decoded
	}
	lower := strings.ToLower(raw)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return "", fmt.Errorf("only local media files can be opened in a local player")
	}
	var base, relative string
	switch {
	case strings.HasPrefix(raw, "/kazumi-files/"):
		base = controllers.KazumiFilesDir()
		relative = strings.TrimPrefix(raw, "/kazumi-files/")
	case strings.HasPrefix(raw, "/bangumi/"):
		base = config.Cfg.SavePath
		relative = strings.TrimPrefix(raw, "/bangumi/")
	default:
		base = config.Cfg.SavePath
		relative = strings.TrimPrefix(raw, "/")
	}
	relative = filepath.FromSlash(relative)
	if relative == "" || filepath.IsAbs(relative) {
		return "", fmt.Errorf("invalid media path")
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	target := filepath.Join(absBase, relative)
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("media path escapes the media directory")
	}
	info, err := os.Stat(absTarget)
	if err != nil {
		return "", fmt.Errorf("media file not found: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("media path is a directory")
	}
	return absTarget, nil
}

func launchLocalPlayer(path string) (string, error) {
	if parts := playerCommandParts(); len(parts) > 0 {
		return startPlayer(parts[0], withPlayerPath(parts[1:], path))
	}
	for _, name := range playerCandidates() {
		if _, err := exec.LookPath(name); err == nil {
			return startPlayer(name, []string{path})
		}
	}
	if runtime.GOOS == "windows" {
		return startPlayer("cmd", []string{"/c", "start", "", path})
	}
	return "", fmt.Errorf("no local player found; set [player] command in %s", config.ConfigFilePath)
}

func playerCommandParts() []string {
	command := strings.TrimSpace(config.Cfg.Player.Command)
	if command == "" {
		return nil
	}
	return splitCommandLine(command)
}

func splitCommandLine(input string) []string {
	var out []string
	var current strings.Builder
	quote := rune(0)
	flush := func() {
		if current.Len() > 0 {
			out = append(out, current.String())
			current.Reset()
		}
	}
	for _, r := range input {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return out
}

func withPlayerPath(args []string, path string) []string {
	if len(args) == 0 {
		return []string{path}
	}
	replaced := false
	out := make([]string, 0, len(args)+1)
	for _, arg := range args {
		if strings.Contains(arg, "{path}") {
			arg = strings.ReplaceAll(arg, "{path}", path)
			replaced = true
		}
		out = append(out, arg)
	}
	if !replaced {
		out = append(out, path)
	}
	return out
}

func playerCandidates() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{"mpv.exe", "vlc.exe", "PotPlayerMini64.exe", "PotPlayerMini.exe"}
	case "darwin":
		return []string{"iina", "mpv", "vlc", "open"}
	default:
		return []string{"mpv", "vlc", "celluloid", "smplayer", "xdg-open"}
	}
}

func startPlayer(name string, args []string) (string, error) {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("failed to start local player %s: %w", name, err)
	}
	go func() { _ = cmd.Wait() }()
	return strings.Join(append([]string{name}, args...), " "), nil
}
