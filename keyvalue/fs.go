package keyvalue

import (
	"errors"
	"path"
	"time"

	"github.com/hack-pad/hackpadfs"
)

type FS struct {
	store Store
}

func NewFS(store Store) (*FS, error) {
	return &FS{
		store: store,
	}, nil
}

func (fs *FS) wrapperErr(op string, path string, err error) error {
	if err == nil {
		return nil
	}
	if uErr, ok := err.(interface{ Unwrap() error }); ok && uErr != nil {
		err = uErr.Unwrap()
	}
	return &hackpadfs.PathError{Op: op, Path: path, Err: err}
}

func (fs *FS) Mkdir(name string, perm hackpadfs.FileMode) error {
	file := fs.newDir(name, perm)
	_, err := fs.Stat(name)
	if errors.Is(err, hackpadfs.ErrNotExist) {
		return fs.wrapperErr("mkdir", name, file.save())
	}
	if err != nil {
		return err
	}
	return fs.wrapperErr("mkdir", name, hackpadfs.ErrExist)
}

func (fs *FS) newDir(name string, perm hackpadfs.FileMode) *file {
	return fs.newFile(name, 0, hackpadfs.ModeDir|(perm&hackpadfs.ModePerm))
}

func (fs *FS) MkdirAll(path string, perm hackpadfs.FileMode) error {
	missingDirs, err := fs.findMissingDirs(path)
	if err != nil {
		return err
	}
	for i := len(missingDirs) - 1; i >= 0; i-- { // missingDirs are in reverse order
		name := missingDirs[i]
		file := fs.newDir(name, perm)
		err := file.save()
		err = fs.wrapperErr("mkdirall", name, err)
		if err != nil && !errors.Is(err, hackpadfs.ErrExist) {
			return err
		}
	}
	return nil
}

func statAll(store Store, paths []string) ([]hackpadfs.FileInfo, []error) {
	infos := make([]hackpadfs.FileInfo, len(paths))
	errs := make([]error, len(paths))
	results := getFileRecords(store, paths)
	for i := range paths {
		path := paths[i]
		result := results[i]
		infos[i], errs[i] = fileInfo{Record: result.Record, Path: path}, result.Err
	}
	return infos, errs
}

// findMissingDirs returns all paths that must be created, in reverse order
func (fs *FS) findMissingDirs(name string) ([]string, error) {
	if !hackpadfs.ValidPath(name) {
		return nil, hackpadfs.ErrInvalid
	}
	const fsRootPath = "."
	var paths []string
	for currentPath := name; currentPath != fsRootPath; currentPath = path.Dir(currentPath) {
		paths = append(paths, currentPath)
	}
	paths = append(paths, fsRootPath)
	infos, errs := statAll(fs.store, paths)

	var missingDirs []string
	for i := range paths {
		missing, err := isMissingDir(paths[i], infos[i], errs[i])
		if err != nil {
			return nil, err
		}
		if missing {
			missingDirs = append(missingDirs, paths[i])
		} else {
			return missingDirs, nil
		}
	}
	return missingDirs, nil
}

func isMissingDir(path string, info hackpadfs.FileInfo, err error) (missing bool, returnedErr error) {
	switch {
	case errors.Is(err, hackpadfs.ErrNotExist):
		return true, nil
	case err != nil:
		return false, err
	case info.IsDir():
		// found a directory in the chain, return early
		return false, nil
	case !info.IsDir():
		// a file is found where we want a directory, fail with ENOTDIR
		return true, &hackpadfs.PathError{Op: "mkdirall", Path: path, Err: hackpadfs.ErrNotDir}
	default:
		return false, nil
	}
}

func (fs *FS) Open(name string) (hackpadfs.File, error) {
	return fs.OpenFile(name, hackpadfs.FlagReadOnly, 0)
}

func (fs *FS) OpenFile(name string, flag int, perm hackpadfs.FileMode) (afFile hackpadfs.File, retErr error) {
	paths := []string{name}
	if flag&hackpadfs.FlagCreate != 0 {
		paths = append(paths, path.Dir(name))
	}
	files, errs := fs.getFiles(paths...)
	storeFile, err := files[0], errs[0]
	switch {
	case err == nil:
		if storeFile.info().IsDir() && flag&(hackpadfs.FlagCreate|hackpadfs.FlagWriteOnly) != 0 {
			// write-only or create on a directory isn't allowed on hackpadfs.OpenFile
			return nil, &hackpadfs.PathError{Op: "open", Path: name, Err: hackpadfs.ErrIsDir}
		}
		storeFile.flag = flag
	case errors.Is(err, hackpadfs.ErrNotExist) && flag&hackpadfs.FlagCreate != 0:
		// require parent directory
		err := errs[1]
		if err != nil {
			return nil, fs.wrapperErr("stat", name, err)
		}
		storeFile = fs.newFile(name, flag, perm&hackpadfs.ModePerm)
		if err := storeFile.save(); err != nil {
			return nil, fs.wrapperErr("open", name, err)
		}
	default:
		return nil, fs.wrapperErr("open", name, err)
	}

	var file hackpadfs.File = storeFile
	switch {
	case flag&hackpadfs.FlagWriteOnly != 0:
		file = &writeOnlyFile{storeFile}
	case flag&hackpadfs.FlagReadWrite != 0:
	default:
		// hackpadfs.FlagReadOnly = 0
		file = &readOnlyFile{storeFile}
	}

	if flag&hackpadfs.FlagTruncate != 0 {
		return file, fs.wrapperErr("open", name, hackpadfs.TruncateFile(file, 0))
	}
	return file, nil
}

func (fs *FS) Remove(name string) error {
	file, err := fs.getFile(name)
	if err != nil {
		return fs.wrapperErr("remove", name, err)
	}

	if file.Mode().IsDir() {
		dirNames, err := file.ReadDirNames()
		if err != nil {
			return err
		}
		if len(dirNames) != 0 {
			return &hackpadfs.PathError{Op: "remove", Path: name, Err: hackpadfs.ErrNotEmpty}
		}
	}
	return fs.setFile(name, nil)
}

func (fs *FS) RemoveAll(path string) error {
	return &hackpadfs.PathError{Op: "removeall", Path: path, Err: hackpadfs.ErrUnsupported}
}

func (fs *FS) Rename(oldname, newname string) error {
	oldFile, err := fs.getFile(oldname)
	if err != nil {
		return &hackpadfs.LinkError{Op: "rename", Old: oldname, New: newname, Err: hackpadfs.ErrNotExist}
	}
	oldInfo, err := oldFile.Stat()
	if err != nil {
		return err
	}
	if !oldInfo.IsDir() {
		err := fs.setFile(newname, oldFile.fileData)
		if err != nil {
			return err
		}
		return fs.setFile(oldname, nil)
	}

	_, err = fs.getFile(newname)
	if !errors.Is(err, hackpadfs.ErrNotExist) {
		return &hackpadfs.LinkError{Op: "rename", Old: oldname, New: newname, Err: hackpadfs.ErrExist}
	}

	files, err := oldFile.ReadDirNames()
	if err != nil {
		return err
	}
	err = fs.setFile(newname, oldFile.fileData)
	if err != nil {
		return err
	}
	for _, name := range files {
		err := fs.Rename(path.Join(oldname, name), path.Join(newname, name))
		if err != nil {
			// TODO don't leave destination in corrupted state (missing file records for dir names)
			return err
		}
	}
	return fs.setFile(oldname, nil)
}

func (fs *FS) Stat(name string) (hackpadfs.FileInfo, error) {
	file, err := fs.getFile(name)
	if err != nil {
		return nil, fs.wrapperErr("stat", name, err)
	}
	return file.info(), nil
}

func (fs *FS) Chmod(name string, mode hackpadfs.FileMode) error {
	file, err := fs.getFile(name)
	if err != nil {
		return fs.wrapperErr("chmod", name, err)
	}

	const chmodBits = hackpadfs.ModePerm | hackpadfs.ModeSetuid | hackpadfs.ModeSetgid | hackpadfs.ModeSticky // Only a subset of bits are allowed to be changed. Documented under os.Chmod()
	newMode := (file.Mode() & ^chmodBits) | (mode & chmodBits)
	file.modeOverride = &newMode
	return file.save()
}

func (fs *FS) Chtimes(name string, atime time.Time, mtime time.Time) error {
	file, err := fs.getFile(name)
	if err != nil {
		return fs.wrapperErr("chtimes", name, err)
	}
	file.modTimeOverride = mtime
	return file.save()
}