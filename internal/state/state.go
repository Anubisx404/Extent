package state

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

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
