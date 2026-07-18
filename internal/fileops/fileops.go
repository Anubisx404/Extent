package fileops

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Anubisx404/Extent/internal/state"
)

var (
	ErrUnsafePath = errors.New("unsafe path")
	ErrConflict   = errors.New("filesystem conflict")
	ErrLocked     = errors.New("project mutation is locked")
)

type Action string

const (
	Create Action = "create"
	Update Action = "update"
	Delete Action = "delete"
)

type Step struct {
	Path   string
	Action Action
	Data   []byte
	Mode   os.FileMode
}

type Plan struct {
	Root  string
	Kind  string
	Steps []Step
}

func NewPlan(root, kind string, steps []Step) (Plan, error) {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(kind) == "" {
		return Plan{}, fmt.Errorf("root and operation kind are required")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return Plan{}, err
	}
	if info, err := os.Lstat(absoluteRoot); err != nil {
		return Plan{}, err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return Plan{}, ErrUnsafePath
	}

	copied := make([]Step, len(steps))
	seen := map[string]bool{}
	for i, step := range steps {
		clean := filepath.Clean(step.Path)
		if step.Path == "" || clean == "." || filepath.IsAbs(step.Path) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return Plan{}, ErrUnsafePath
		}
		switch step.Action {
		case Create, Update, Delete:
		default:
			return Plan{}, fmt.Errorf("unsupported action %q", step.Action)
		}
		if seen[clean] {
			return Plan{}, fmt.Errorf("duplicate target %q", clean)
		}
		seen[clean] = true
		copied[i] = step
		copied[i].Path = clean
		copied[i].Data = append([]byte(nil), step.Data...)
	}
	sort.Slice(copied, func(i, j int) bool { return copied[i].Path < copied[j].Path })
	return Plan{Root: absoluteRoot, Kind: strings.TrimSpace(kind), Steps: copied}, nil
}

type Options struct {
	// FailAfter injects a failure before applying the step at this zero-based
	// count. Values <= 0 disable injection.
	FailAfter int
}

type snapshot struct {
	path    string
	existed bool
	data    []byte
	mode    os.FileMode
}

type preparedStep struct {
	step     Step
	fullPath string
	before   snapshot
}

type ownership struct {
	after string
}

func Apply(plan Plan) (state.Manifest, error) {
	return ApplyWithOptions(plan, Options{})
}

func ApplyWithOptions(plan Plan, options Options) (manifest state.Manifest, err error) {
	lock, err := acquireLock(plan.Root, plan.Kind)
	if err != nil {
		return manifest, err
	}
	defer releaseLock(lock)

	manifest, err = loadOrInitializeManifest(plan.Root)
	if err != nil {
		return manifest, err
	}
	owned := currentOwnership(manifest)
	prepared := make([]preparedStep, 0, len(plan.Steps))
	for _, step := range plan.Steps {
		full, err := safeTarget(plan.Root, step.Path)
		if err != nil {
			return manifest, err
		}
		before, err := inspect(full)
		if err != nil {
			return manifest, err
		}
		owner, isOwned := owned[step.Path]
		if isOwned && before.existed && state.Hash(before.data) != owner.after {
			return manifest, fmt.Errorf("%w: owned target %s was modified", ErrConflict, step.Path)
		}

		switch step.Action {
		case Create:
			if before.existed {
				if isOwned && state.Hash(before.data) == state.Hash(step.Data) {
					continue
				}
				return manifest, fmt.Errorf("%w: create target %s already exists", ErrConflict, step.Path)
			}
		case Update:
			if !before.existed {
				return manifest, fmt.Errorf("%w: update target %s does not exist", ErrConflict, step.Path)
			}
			if state.Hash(before.data) == state.Hash(step.Data) {
				continue
			}
		case Delete:
			if !before.existed {
				continue
			}
			if !isOwned {
				return manifest, fmt.Errorf("%w: delete target %s is not owned by Extent", ErrConflict, step.Path)
			}
		}
		prepared = append(prepared, preparedStep{step: step, fullPath: full, before: before})
	}
	if len(prepared) == 0 {
		return manifest, nil
	}

	operationID, err := newOperationID()
	if err != nil {
		return manifest, err
	}
	operation := state.Operation{ID: operationID, Kind: plan.Kind}
	applied := make([]snapshot, 0, len(prepared))
	createdDirs := []string{}
	rollback := func(primary error) error {
		var rollbackErrors []error
		for i := len(applied) - 1; i >= 0; i-- {
			if restoreErr := restore(applied[i]); restoreErr != nil {
				rollbackErrors = append(rollbackErrors, restoreErr)
			}
		}
		for i := len(createdDirs) - 1; i >= 0; i-- {
			_ = os.Remove(createdDirs[i])
		}
		_ = removeBackupRoot(plan.Root, operationID)
		if len(rollbackErrors) > 0 {
			return errors.Join(append([]error{primary}, rollbackErrors...)...)
		}
		return primary
	}

	for index, prepared := range prepared {
		if options.FailAfter > 0 && index >= options.FailAfter {
			return manifest, rollback(errors.New("injected failure"))
		}
		step := prepared.step
		entry := state.Entry{
			Path:    step.Path,
			Action:  string(step.Action),
			Existed: prepared.before.existed,
			Mode:    uint32(prepared.before.mode.Perm()),
		}
		if prepared.before.existed {
			entry.Before = state.Hash(prepared.before.data)
			entry.Backup = filepath.Join(".extent", "backups", operationID, step.Path)
			backupPath := filepath.Join(plan.Root, entry.Backup)
			newDirs, err := ensureParents(plan.Root, backupPath, 0o700)
			if err != nil {
				return manifest, rollback(err)
			}
			createdDirs = append(createdDirs, newDirs...)
			if err := atomicWrite(backupPath, prepared.before.data, 0o600); err != nil {
				return manifest, rollback(err)
			}
		}

		applied = append(applied, prepared.before)
		switch step.Action {
		case Create, Update:
			newDirs, err := ensureParents(plan.Root, prepared.fullPath, 0o755)
			if err != nil {
				return manifest, rollback(err)
			}
			createdDirs = append(createdDirs, newDirs...)
			if err := atomicWrite(prepared.fullPath, step.Data, stepMode(step)); err != nil {
				return manifest, rollback(err)
			}
			entry.After = state.Hash(step.Data)
		case Delete:
			if err := os.Remove(prepared.fullPath); err != nil {
				return manifest, rollback(err)
			}
		}
		operation.Entries = append(operation.Entries, entry)
	}

	manifest.Operations = append(manifest.Operations, operation)
	if err := state.Save(plan.Root, manifest); err != nil {
		return manifest, rollback(err)
	}
	return manifest, nil
}

func Undo(root string) error {
	return undo(root, "")
}

func UndoKind(root, kind string) error {
	if strings.TrimSpace(kind) == "" {
		return fmt.Errorf("operation kind is required")
	}
	return undo(root, kind)
}

func undo(root, kind string) error {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	lock, err := acquireLock(absoluteRoot, "undo")
	if err != nil {
		return err
	}
	defer releaseLock(lock)

	manifest, err := state.Load(absoluteRoot)
	if err != nil {
		return err
	}
	operationIndex := -1
	for i := len(manifest.Operations) - 1; i >= 0; i-- {
		if kind == "" || manifest.Operations[i].Kind == kind {
			operationIndex = i
			break
		}
	}
	if operationIndex < 0 {
		return fmt.Errorf("no matching operation to undo")
	}
	operation := manifest.Operations[operationIndex]

	type undoStep struct {
		entry  state.Entry
		full   string
		backup []byte
	}
	steps := make([]undoStep, 0, len(operation.Entries))
	for i := len(operation.Entries) - 1; i >= 0; i-- {
		entry := operation.Entries[i]
		full, err := safeTarget(absoluteRoot, entry.Path)
		if err != nil {
			return err
		}
		current, err := inspect(full)
		if err != nil {
			return err
		}
		step := undoStep{entry: entry, full: full}
		switch Action(entry.Action) {
		case Create:
			if !current.existed || state.Hash(current.data) != entry.After {
				return fmt.Errorf("%w: created target %s was modified or removed", ErrConflict, entry.Path)
			}
		case Update:
			if !current.existed || state.Hash(current.data) != entry.After {
				return fmt.Errorf("%w: updated target %s was modified or removed", ErrConflict, entry.Path)
			}
			step.backup, err = readVerifiedBackup(absoluteRoot, entry)
			if err != nil {
				return err
			}
		case Delete:
			if current.existed {
				return fmt.Errorf("%w: deleted target %s was recreated", ErrConflict, entry.Path)
			}
			step.backup, err = readVerifiedBackup(absoluteRoot, entry)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported undo action %q", entry.Action)
		}
		steps = append(steps, step)
	}

	applied := []snapshot{}
	rollback := func(primary error) error {
		var rollbackErrors []error
		for i := len(applied) - 1; i >= 0; i-- {
			if restoreErr := restore(applied[i]); restoreErr != nil {
				rollbackErrors = append(rollbackErrors, restoreErr)
			}
		}
		if len(rollbackErrors) > 0 {
			return errors.Join(append([]error{primary}, rollbackErrors...)...)
		}
		return primary
	}
	for _, step := range steps {
		beforeUndo, err := inspect(step.full)
		if err != nil {
			return rollback(err)
		}
		applied = append(applied, beforeUndo)
		switch Action(step.entry.Action) {
		case Create:
			if err := os.Remove(step.full); err != nil {
				return rollback(err)
			}
		case Update, Delete:
			if _, err := ensureParents(absoluteRoot, step.full, 0o755); err != nil {
				return rollback(err)
			}
			if err := atomicWrite(step.full, step.backup, os.FileMode(step.entry.Mode)); err != nil {
				return rollback(err)
			}
		}
	}

	manifest.Operations = append(manifest.Operations[:operationIndex], manifest.Operations[operationIndex+1:]...)
	if len(manifest.Operations) == 0 {
		if err := state.Remove(absoluteRoot); err != nil {
			return rollback(err)
		}
	} else {
		if err := state.Save(absoluteRoot, manifest); err != nil {
			return rollback(err)
		}
	}
	_ = removeBackupRoot(absoluteRoot, operation.ID)
	if len(manifest.Operations) == 0 {
		removeEmptyParents(absoluteRoot, filepath.Join(absoluteRoot, ".extent", "backups"))
		removeEmptyParents(absoluteRoot, filepath.Join(absoluteRoot, ".extent"))
	}
	for _, step := range steps {
		if Action(step.entry.Action) == Create {
			removeEmptyParents(absoluteRoot, filepath.Dir(step.full))
		}
	}
	return nil
}

func currentOwnership(manifest state.Manifest) map[string]ownership {
	owned := map[string]ownership{}
	for _, operation := range manifest.Operations {
		for _, entry := range operation.Entries {
			switch Action(entry.Action) {
			case Create, Update:
				owned[entry.Path] = ownership{after: entry.After}
			case Delete:
				delete(owned, entry.Path)
			}
		}
	}
	return owned
}

func loadOrInitializeManifest(root string) (state.Manifest, error) {
	manifest, err := state.Load(root)
	if err == nil {
		return manifest, nil
	}
	if os.IsNotExist(err) {
		return state.Manifest{Version: state.CurrentVersion}, nil
	}
	return state.Manifest{}, err
}

func inspect(path string) (snapshot, error) {
	result := snapshot{path: path}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return result, ErrUnsafePath
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	result.existed = true
	result.data = data
	result.mode = info.Mode().Perm()
	return result, nil
}

func safeTarget(root, relative string) (string, error) {
	full := filepath.Join(root, relative)
	if !contained(root, full) {
		return "", ErrUnsafePath
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrUnsafePath
	}
	current := root
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", ErrUnsafePath
		}
		if i < len(parts)-1 && !info.IsDir() {
			return "", ErrUnsafePath
		}
	}
	return full, nil
}

func contained(root, path string) bool {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(absoluteRoot, absolutePath)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func ensureParents(root, target string, mode os.FileMode) ([]string, error) {
	parent := filepath.Dir(target)
	if !contained(root, parent) {
		return nil, ErrUnsafePath
	}
	missing := []string{}
	for current := parent; current != root; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return nil, ErrUnsafePath
			}
			break
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		missing = append(missing, current)
	}
	if err := os.MkdirAll(parent, mode); err != nil {
		return nil, err
	}
	for left, right := 0, len(missing)-1; left < right; left, right = left+1, right-1 {
		missing[left], missing[right] = missing[right], missing[left]
	}
	return missing, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".extent-stage-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode.Perm()); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}

	oldPath := temporaryPath + ".old"
	hadOld := false
	if _, err := os.Lstat(path); err == nil {
		if err := os.Rename(path, oldPath); err != nil {
			return err
		}
		hadOld = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		if hadOld {
			_ = os.Rename(oldPath, path)
		}
		return err
	}
	if hadOld {
		_ = os.Remove(oldPath)
	}
	return os.Chmod(path, mode.Perm())
}

func restore(value snapshot) error {
	if value.existed {
		return atomicWrite(value.path, value.data, value.mode)
	}
	if err := os.Remove(value.path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func readVerifiedBackup(root string, entry state.Entry) ([]byte, error) {
	if entry.Backup == "" {
		return nil, fmt.Errorf("missing backup for %s", entry.Path)
	}
	backupPath, err := safeTarget(root, entry.Backup)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(backupPath)
	if err != nil {
		return nil, err
	}
	if state.Hash(data) != entry.Before {
		return nil, fmt.Errorf("%w: backup checksum mismatch for %s", ErrConflict, entry.Path)
	}
	return data, nil
}

func removeBackupRoot(root, operationID string) error {
	if operationID == "" || strings.ContainsAny(operationID, `/\\`) {
		return ErrUnsafePath
	}
	relative := filepath.Join(".extent", "backups", operationID)
	path, err := safeTarget(root, relative)
	if err != nil {
		return err
	}
	return os.RemoveAll(path)
}

func stepMode(step Step) os.FileMode {
	if step.Mode != 0 {
		return step.Mode.Perm()
	}
	return 0o644
}

func newOperationID() (string, error) {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func acquireLock(root, operation string) (*os.File, error) {
	directory, err := safeTarget(root, ".extent")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLocked, err)
	}
	if _, err := fmt.Fprintf(file, "pid=%d\noperation=%s\nstarted=%s\n", os.Getpid(), operation, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	return file, nil
}

func releaseLock(file *os.File) {
	if file == nil {
		return
	}
	path := file.Name()
	_ = file.Close()
	_ = os.Remove(path)
}

func removeEmptyParents(root, directory string) {
	for directory != root && contained(root, directory) {
		if err := os.Remove(directory); err != nil {
			return
		}
		directory = filepath.Dir(directory)
	}
}
