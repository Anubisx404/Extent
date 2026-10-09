package state

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// CurrentVersion is the state manifest schema version written by this build.
// Manifests written by v1.0.x carry no version field; they decode as version 0
// and are treated as version 1 on load.
const CurrentVersion = 1

type Entry struct {
	Path    string `json:"path"`
	Action  string `json:"action"`
	Before  string `json:"before,omitempty"`
	After   string `json:"after,omitempty"`
	Mode    uint32 `json:"mode,omitempty"`
	Existed bool   `json:"existed,omitempty"`
	Backup  string `json:"backup,omitempty"`
}

type Operation struct {
	ID      string  `json:"id"`
	Kind    string  `json:"kind"`
	Entries []Entry `json:"entries"`
}

type Manifest struct {
	Version    int         `json:"version"`
	Operations []Operation `json:"operations"`
}

func Hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func Save(root string, m Manifest) error {
	if m.Version == 0 {
		m.Version = CurrentVersion
	}
	if err := Validate(m); err != nil {
		return err
	}
	d, err := stateDirectory(root, true)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(d, ".state-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	path := filepath.Join(d, "state.json")
	previous := filepath.Join(d, ".state-previous")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		if _, previousErr := os.Lstat(previous); previousErr == nil {
			if renameErr := os.Rename(previous, path); renameErr != nil {
				return renameErr
			}
		}
	}
	_ = os.Remove(previous)
	hadPrevious := false
	if _, err := os.Lstat(path); err == nil {
		if err := os.Rename(path, previous); err != nil {
			return err
		}
		hadPrevious = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		if hadPrevious {
			_ = os.Rename(previous, path)
		}
		return err
	}
	if hadPrevious {
		_ = os.Remove(previous)
	}
	return nil
}

func Load(root string) (Manifest, error) {
	var m Manifest
	directory, err := stateDirectory(root, false)
	if err != nil {
		return m, err
	}
	b, err := os.ReadFile(filepath.Join(directory, "state.json"))
	if os.IsNotExist(err) {
		b, err = os.ReadFile(filepath.Join(directory, ".state-previous"))
	}
	if err != nil {
		return m, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("invalid state: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return m, fmt.Errorf("invalid state: multiple JSON values")
	}
	if m.Version == 0 {
		// Missing version: written by v1.0.x, which predates the version field.
		m.Version = CurrentVersion
	}
	if m.Version > CurrentVersion {
		return Manifest{}, fmt.Errorf("this state (%s) was written by a newer Extent (state version %d, this build supports up to %d); upgrade Extent", filepath.Join(directory, "state.json"), m.Version, CurrentVersion)
	}
	if err := Validate(m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Remove deletes the transactional manifest after the final operation has been
// undone. It never follows a symlinked state directory.
func Remove(root string) error {
	directory, err := stateDirectory(root, false)
	if err != nil {
		return err
	}
	for _, name := range []string{"state.json", ".state-previous"} {
		path := filepath.Join(directory, name)
		info, statErr := os.Lstat(path)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return statErr
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("unsafe state file")
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}

func Validate(m Manifest) error {
	if m.Version != CurrentVersion {
		return fmt.Errorf("unsupported state version %d", m.Version)
	}
	operationIDs := map[string]bool{}
	for _, operation := range m.Operations {
		if strings.TrimSpace(operation.ID) == "" || strings.TrimSpace(operation.Kind) == "" {
			return fmt.Errorf("state operation id and kind are required")
		}
		if operationIDs[operation.ID] {
			return fmt.Errorf("duplicate state operation %q", operation.ID)
		}
		operationIDs[operation.ID] = true
		paths := map[string]bool{}
		for _, entry := range operation.Entries {
			clean := filepath.Clean(entry.Path)
			if entry.Path == "" || entry.Path == "." || filepath.IsAbs(entry.Path) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
				return fmt.Errorf("unsafe state path %q", entry.Path)
			}
			if err := ValidateRelativePath(entry.Path); err != nil {
				return fmt.Errorf("unsafe state path %q: %w", entry.Path, err)
			}
			if paths[clean] {
				return fmt.Errorf("duplicate state path %q", clean)
			}
			paths[clean] = true
			switch entry.Action {
			case "create", "update", "delete":
			default:
				return fmt.Errorf("unsupported state action %q", entry.Action)
			}
			if entry.Backup != "" {
				backup := filepath.Clean(entry.Backup)
				prefix := filepath.Join(".extent", "backups", operation.ID) + string(filepath.Separator)
				if filepath.IsAbs(entry.Backup) || !strings.HasPrefix(backup, prefix) {
					return fmt.Errorf("unsafe backup path %q", entry.Backup)
				}
			}
		}
	}
	return nil
}

func stateDirectory(root string, create bool) (string, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rootInfo, err := os.Lstat(absoluteRoot)
	if err != nil {
		return "", err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("unsafe project root")
	}
	directory := filepath.Join(absoluteRoot, ".extent")
	info, err := os.Lstat(directory)
	if err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("unsafe state directory")
		}
		return directory, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	if !create {
		return "", err
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		return "", err
	}
	return directory, nil
}

// ErrUnsafeRelativePath reports a project-relative path that could escape the
// project root or that cannot be created portably.
var ErrUnsafeRelativePath = errors.New("unsafe relative path")

var reservedWindowsNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true, "CONIN$": true, "CONOUT$": true,
}

// ValidateRelativePath enforces the path policy for every project-relative
// path Extent writes or records: it must be non-empty, relative, free of ".."
// components, and must not name a Windows reserved device (CON, PRN, AUX, NUL,
// COM1-9, LPT1-9, with or without an extension) in any component. The reserved
// names are refused on every OS so a project stays portable. Both "/" and "\\"
// are treated as separators.
func ValidateRelativePath(p string) error {
	if strings.TrimSpace(p) == "" {
		return fmt.Errorf("%w: empty path", ErrUnsafeRelativePath)
	}
	normalized := strings.ReplaceAll(p, `\`, "/")
	if filepath.IsAbs(p) || strings.HasPrefix(normalized, "/") || filepath.VolumeName(p) != "" {
		return fmt.Errorf("%w: %q is absolute", ErrUnsafeRelativePath, p)
	}
	for _, part := range strings.Split(normalized, "/") {
		if part == ".." {
			return fmt.Errorf("%w: %q contains a parent directory component", ErrUnsafeRelativePath, p)
		}
	}
	clean := path.Clean(normalized)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("%w: %q escapes the project root", ErrUnsafeRelativePath, p)
	}
	for _, part := range strings.Split(clean, "/") {
		if isReservedWindowsName(part) {
			return fmt.Errorf("%w: %q uses reserved Windows name %q", ErrUnsafeRelativePath, p, part)
		}
	}
	return nil
}

func isReservedWindowsName(component string) bool {
	base := component
	if dot := strings.IndexByte(base, '.'); dot >= 0 {
		base = base[:dot]
	}
	base = strings.ToUpper(strings.TrimRight(base, " "))
	if reservedWindowsNames[base] {
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return true
	}
	return false
}
