package fstest

import (
	// Avoid importing "os" package in fstest if we can, since not all environments may be able to support it.
	// Not to mention it should compile a little faster. :)
	"errors"
	"io"
	"sort"
	"testing"

	"github.com/hack-pad/hackpadfs"
	"github.com/hack-pad/hackpadfs/internal/assert"
)

func TestFileClose(tb testing.TB, o FSOptions) {
	setupFS, commit := o.Setup.FS(tb)
	f, err := hackpadfs.Create(setupFS, "foo")
	if assert.NoError(tb, err) {
		assert.NoError(tb, f.Close())
	}

	fs := commit()
	f, err = fs.Open("foo")
	if assert.NoError(tb, err) {
		assert.NoError(tb, f.Close())
		assert.Error(tb, f.Close())
	}
}

func TestFileRead(tb testing.TB, o FSOptions) {
	const fileContents = "hello world"
	o.tbRun(tb, "read empty", func(tb testing.TB) {
		setupFS, commit := o.Setup.FS(tb)
		f, err := hackpadfs.Create(setupFS, "foo")
		if assert.NoError(tb, err) {
			assert.NoError(tb, f.Close())
		}

		fs := commit()
		f, err = fs.Open("foo")
		if !assert.NoError(tb, err) {
			tb.FailNow()
		}
		buf := make([]byte, 10)
		n, err := f.Read(buf)
		assert.Equal(tb, 0, n)
		assert.Equal(tb, io.EOF, err)
		assert.NoError(tb, f.Close())
	})

	o.tbRun(tb, "read a few bytes at a time", func(tb testing.TB) {
		setupFS, commit := o.Setup.FS(tb)
		const firstBufLen = 2
		f, err := hackpadfs.Create(setupFS, "foo")
		if assert.NoError(tb, err) {
			_, err = hackpadfs.WriteFile(f, []byte(fileContents))
			assert.NoError(tb, err)
			assert.NoError(tb, f.Close())
		}

		fs := commit()
		f, err = fs.Open("foo")
		if !assert.NoError(tb, err) {
			tb.FailNow()
		}

		buf := make([]byte, firstBufLen)
		n, err := f.Read(buf)
		assert.Equal(tb, firstBufLen, n)
		assert.NoError(tb, err)
		assert.Equal(tb, "he", string(buf))

		buf = make([]byte, len(fileContents)*2)
		n, err = f.Read(buf)
		assert.Equal(tb, len(fileContents)-firstBufLen, n)
		if err == nil {
			// it's ok to return a nil error when finishing a read
			// but the next read must return 0 and EOF
			tmpBuf := make([]byte, len(buf))
			var zeroN int
			zeroN, err = f.Read(tmpBuf)
			assert.Equal(tb, 0, zeroN)
		}
		assert.Equal(tb, io.EOF, err)
		assert.Equal(tb, "llo world", string(buf[:n]))
		assert.NoError(tb, f.Close())
	})
}

func TestFileReadAt(tb testing.TB, o FSOptions) {
	const fileContents = "hello world"
	setupFS, commit := o.Setup.FS(tb)
	f, err := hackpadfs.Create(setupFS, "foo")
	if assert.NoError(tb, err) {
		_, err = hackpadfs.WriteFile(f, []byte(fileContents))
		assert.NoError(tb, err)
		assert.NoError(tb, f.Close())
	}
	fs := commit()

	for _, tc := range []struct {
		description string
		bufSize     int
		off         int64
		expectN     int
		expectBuf   string
		expectErr   error
	}{
		{
			description: "at start",
			bufSize:     len(fileContents),
			off:         0,
			expectN:     len(fileContents),
			expectBuf:   "hello world",
		},
		{
			description: "negative offset",
			bufSize:     len(fileContents),
			off:         -1,
			expectErr:   errors.New("negative offset"),
		},
		{
			description: "small byte offset",
			bufSize:     len(fileContents),
			off:         2,
			expectN:     len(fileContents) - 2,
			expectBuf:   "llo world",
			expectErr:   io.EOF,
		},
		{
			description: "small read at offset",
			bufSize:     2,
			off:         2,
			expectN:     2,
			expectBuf:   "ll",
		},
		{
			description: "full read at offset",
			bufSize:     len(fileContents),
			off:         2,
			expectN:     len(fileContents) - 2,
			expectBuf:   "llo world",
			expectErr:   io.EOF,
		},
	} {
		o.tbRun(tb, tc.description, func(tb testing.TB) {
			tbParallel(tb)
			file, err := fs.Open("foo")
			if !assert.NoError(tb, err) {
				return
			}
			defer func() {
				assert.NoError(tb, file.Close())
			}()
			f, ok := file.(hackpadfs.ReaderAtFile)
			if !ok {
				tb.Skip("File is not a ReaderAtFile")
			}

			buf := make([]byte, tc.bufSize)
			n, err := f.ReadAt(buf, tc.off)
			if n == tc.bufSize && err == io.EOF {
				err = nil
			}
			if tc.expectErr != nil {
				if err, ok := err.(*hackpadfs.PathError); ok {
					assert.Equal(tb, "readat", err.Op)
					assert.Equal(tb, "foo", err.Path)
				}
				assert.ErrorIs(tb, tc.expectErr, err)
				return
			}
			assert.NoError(tb, err)
			assert.Equal(tb, tc.expectN, n)
			assert.Equal(tb, tc.expectBuf, string(buf[:n]))
		})
	}
}

func TestFileSeek(tb testing.TB, o FSOptions) {
	const fileContents = "hello world"

	seekFile := func(tb testing.TB, file hackpadfs.File) hackpadfs.SeekerFile {
		if file, ok := file.(hackpadfs.SeekerFile); ok {
			return file
		}
		tb.Skip("File is not a SeekerFile")
		return nil
	}

	o.tbRun(tb, "seek unknown start", func(tb testing.TB) {
		setupFS, commit := o.Setup.FS(tb)
		file, err := hackpadfs.Create(setupFS, "foo")
		if assert.NoError(tb, err) {
			_, err = hackpadfs.WriteFile(file, []byte(fileContents))
			assert.NoError(tb, err)
			assert.NoError(tb, file.Close())
		}

		fs := commit()
		file, err = fs.Open("foo")
		assert.NoError(tb, err)
		f := seekFile(tb, file)
		_, err = f.Seek(0, -1)
		if assert.IsType(tb, &hackpadfs.PathError{}, err) {
			err := err.(*hackpadfs.PathError)
			assert.Equal(tb, "seek", err.Op)
			assert.Equal(tb, "foo", err.Path)
			assert.ErrorIs(tb, hackpadfs.ErrInvalid, err)
		}
		assert.NoError(tb, f.Close())
	})

	o.tbRun(tb, "seek negative offset", func(tb testing.TB) {
		setupFS, commit := o.Setup.FS(tb)
		file, err := hackpadfs.Create(setupFS, "foo")
		if assert.NoError(tb, err) {
			_, err = hackpadfs.WriteFile(file, []byte(fileContents))
			assert.NoError(tb, err)
			assert.NoError(tb, file.Close())
		}

		fs := commit()
		file, err = fs.Open("foo")
		assert.NoError(tb, err)
		f := seekFile(tb, file)
		_, err = f.Seek(-1, io.SeekStart)
		if assert.IsType(tb, &hackpadfs.PathError{}, err) {
			err := err.(*hackpadfs.PathError)
			assert.Equal(tb, "seek", err.Op)
			assert.Equal(tb, "foo", err.Path)
			assert.ErrorIs(tb, hackpadfs.ErrInvalid, err)
		}
		assert.NoError(tb, f.Close())
	})

	o.tbRun(tb, "seek start", func(tb testing.TB) {
		setupFS, commit := o.Setup.FS(tb)
		file, err := hackpadfs.Create(setupFS, "foo")
		if assert.NoError(tb, err) {
			_, err = hackpadfs.WriteFile(file, []byte(fileContents))
			assert.NoError(tb, err)
			assert.NoError(tb, file.Close())
		}

		fs := commit()
		file, err = fs.Open("foo")
		assert.NoError(tb, err)
		f := seekFile(tb, file)
		const offset = 1
		off, err := f.Seek(offset, io.SeekStart)
		assert.NoError(tb, err)
		assert.Equal(tb, int64(offset), off)
		buf := make([]byte, len(fileContents))
		n, err := f.Read(buf)
		assert.Equal(tb, true, err == nil || err == io.EOF)
		assert.Equal(tb, "ello world", string(buf[:n]))
		assert.NoError(tb, f.Close())
	})

	o.tbRun(tb, "seek current", func(tb testing.TB) {
		setupFS, commit := o.Setup.FS(tb)
		file, err := hackpadfs.Create(setupFS, "foo")
		if assert.NoError(tb, err) {
			_, err = hackpadfs.WriteFile(file, []byte(fileContents))
			assert.NoError(tb, err)
			assert.NoError(tb, file.Close())
		}

		fs := commit()
		file, err = fs.Open("foo")
		assert.NoError(tb, err)
		f := seekFile(tb, file)
		const firstSeekOff = 5
		const offset = -1
		_, err = f.Seek(firstSeekOff, io.SeekStart) // get close to middle
		assert.NoError(tb, err)
		off, err := f.Seek(offset, io.SeekCurrent)
		assert.NoError(tb, err)
		assert.Equal(tb, int64(firstSeekOff+offset), off)
		buf := make([]byte, len(fileContents))
		n, err := f.Read(buf)
		assert.Equal(tb, true, err == nil || err == io.EOF)
		assert.Equal(tb, "o world", string(buf[:n]))
		assert.NoError(tb, f.Close())
	})

	o.tbRun(tb, "seek end", func(tb testing.TB) {
		setupFS, commit := o.Setup.FS(tb)
		file, err := hackpadfs.Create(setupFS, "foo")
		if assert.NoError(tb, err) {
			_, err = hackpadfs.WriteFile(file, []byte(fileContents))
			assert.NoError(tb, err)
			assert.NoError(tb, file.Close())
		}

		fs := commit()
		file, err = fs.Open("foo")
		assert.NoError(tb, err)
		f := seekFile(tb, file)
		const offset = -1
		off, err := f.Seek(offset, io.SeekEnd)
		assert.NoError(tb, err)
		assert.Equal(tb, int64(len(fileContents)+offset), off)
		buf := make([]byte, len(fileContents))
		n, err := f.Read(buf)
		assert.Equal(tb, true, err == nil || err == io.EOF)
		assert.Equal(tb, "d", string(buf[:n]))
		assert.NoError(tb, f.Close())
	})
}

func TestFileWrite(tb testing.TB, o FSOptions) {
	testFileWrite(tb, o, func(file hackpadfs.File, b []byte) (int, error) {
		if file, ok := file.(hackpadfs.ReadWriterFile); ok {
			return file.Write(b)
		}
		tb.Skip("File is not a ReadWriterFile")
		return 0, nil
	})
}

func testFileWrite(tb testing.TB, o FSOptions, writer func(hackpadfs.File, []byte) (int, error)) {
	o.tbRun(tb, "write-read", func(tb testing.TB) {
		const fileContents = "hello world"
		setupFS, commit := o.Setup.FS(tb)
		f, err := hackpadfs.Create(setupFS, "foo")
		if assert.NoError(tb, err) {
			assert.NoError(tb, f.Close())
		}

		fs, ok := commit().(hackpadfs.OpenFileFS)
		if !ok {
			tb.Skip("FS is not an OpenFileFS")
		}
		f, err = fs.OpenFile("foo", hackpadfs.FlagReadWrite, 0)
		assert.NoError(tb, err)
		n, err := writer(f, []byte(fileContents))
		assert.Equal(tb, len(fileContents), n)
		assert.NoError(tb, err)
		assert.NoError(tb, f.Close())
		f, err = fs.Open("foo")
		assert.NoError(tb, err)
		buf := make([]byte, len(fileContents))
		_, _ = f.Read(buf)
		assert.Equal(tb, fileContents, string(buf))
		assert.NoError(tb, f.Close())
	})

	o.tbRun(tb, "write-truncate-write-read", func(tb testing.TB) {
		const fileContents = "hello world"
		setupFS, commit := o.Setup.FS(tb)
		f, err := hackpadfs.Create(setupFS, "foo")
		if assert.NoError(tb, err) {
			_, err := writer(f, []byte(fileContents))
			assert.NoError(tb, err)
			assert.NoError(tb, f.Close())
		}

		fs, ok := commit().(hackpadfs.OpenFileFS)
		if !ok {
			tb.Skip("FS is not an OpenFileFS")
		}
		f, err = fs.OpenFile("foo", hackpadfs.FlagReadWrite|hackpadfs.FlagTruncate, 0)
		assert.NoError(tb, err)
		n, err := writer(f, []byte(fileContents))
		assert.Equal(tb, len(fileContents), n)
		assert.NoError(tb, err)
		assert.NoError(tb, f.Close())
		f, err = fs.Open("foo")
		assert.NoError(tb, err)
		buf := make([]byte, len(fileContents))
		_, _ = f.Read(buf)
		assert.Equal(tb, fileContents, string(buf))
		assert.NoError(tb, f.Close())
	})
}

func TestFileWriteAt(tb testing.TB, o FSOptions) {
	writeAtFile := func(tb testing.TB, file hackpadfs.File) hackpadfs.WriterAtFile {
		if file, ok := file.(hackpadfs.WriterAtFile); ok {
			return file
		}
		tb.Skip("File is not a WriterAtFile")
		return nil
	}

	o.tbRun(tb, "negative offset", func(tb testing.TB) {
		setupFS, commit := o.Setup.FS(tb)
		file, err := hackpadfs.Create(setupFS, "foo")
		if assert.NoError(tb, err) {
			assert.NoError(tb, file.Close())
		}

		fs, ok := commit().(hackpadfs.OpenFileFS)
		if !ok {
			tb.Skip("FS is not an OpenFileFS")
		}
		file, err = fs.OpenFile("foo", hackpadfs.FlagReadWrite, 0)
		assert.NoError(tb, err)
		f := writeAtFile(tb, file)
		n, err := f.WriteAt([]byte("hello"), -1)
		assert.Equal(tb, 0, n)
		assert.Error(tb, err)
		assert.NoError(tb, f.Close())

		if assert.IsType(tb, &hackpadfs.PathError{}, err) {
			err := err.(*hackpadfs.PathError)
			assert.Equal(tb, "writeat", err.Op)
			assert.Equal(tb, "foo", err.Path)
			assert.Equal(tb, "negative offset", err.Err.Error())
		}
	})

	o.tbRun(tb, "no offset", func(tb testing.TB) {
		setupFS, commit := o.Setup.FS(tb)
		file, err := hackpadfs.Create(setupFS, "foo")
		if assert.NoError(tb, err) {
			assert.NoError(tb, file.Close())
		}

		fs, ok := commit().(hackpadfs.OpenFileFS)
		if !ok {
			tb.Skip("FS is not an OpenFileFS")
		}
		file, err = fs.OpenFile("foo", hackpadfs.FlagReadWrite, 0)
		assert.NoError(tb, err)
		f := writeAtFile(tb, file)
		const fileContents = "hello world"
		n, err := f.WriteAt([]byte(fileContents), 0)
		assert.Equal(tb, len(fileContents), n)
		assert.NoError(tb, err)
		assert.NoError(tb, f.Close())

		file, err = fs.Open("foo")
		assert.NoError(tb, err)
		buf := make([]byte, len(fileContents))
		_, _ = file.Read(buf)
		assert.Equal(tb, fileContents, string(buf))
		assert.NoError(tb, file.Close())
	})

	o.tbRun(tb, "offset inside file", func(tb testing.TB) {
		setupFS, commit := o.Setup.FS(tb)
		file, err := hackpadfs.Create(setupFS, "foo")
		if assert.NoError(tb, err) {
			assert.NoError(tb, file.Close())
		}

		fs, ok := commit().(hackpadfs.OpenFileFS)
		if !ok {
			tb.Skip("FS is not an OpenFileFS")
		}
		file, err = fs.OpenFile("foo", hackpadfs.FlagReadWrite, 0)
		assert.NoError(tb, err)
		f := writeAtFile(tb, file)
		const fileContents = "hello world"
		const newContents = "hi"
		const offset = 5
		_, err = f.WriteAt([]byte(fileContents), 0)
		assert.NoError(tb, err)
		n, err := f.WriteAt([]byte(newContents), offset)
		assert.Equal(tb, len(newContents), n)
		assert.NoError(tb, err)
		assert.NoError(tb, f.Close())

		file, err = fs.Open("foo")
		assert.NoError(tb, err)
		buf := make([]byte, len(fileContents))
		_, _ = file.Read(buf)
		assert.Equal(tb, "hellohiorld", string(buf))
		assert.NoError(tb, file.Close())
	})

	o.tbRun(tb, "offset outside file", func(tb testing.TB) {
		setupFS, commit := o.Setup.FS(tb)
		file, err := hackpadfs.Create(setupFS, "foo")
		if assert.NoError(tb, err) {
			assert.NoError(tb, file.Close())
		}

		fs, ok := commit().(hackpadfs.OpenFileFS)
		if !ok {
			tb.Skip("FS is not an OpenFileFS")
		}
		file, err = fs.OpenFile("foo", hackpadfs.FlagReadWrite, 0)
		assert.NoError(tb, err)
		f := writeAtFile(tb, file)
		const fileContents = "hello world"
		const offset = 5
		n, err := f.WriteAt([]byte(fileContents), offset)
		assert.Equal(tb, len(fileContents), n)
		assert.NoError(tb, err)
		assert.NoError(tb, f.Close())

		file, err = fs.Open("foo")
		assert.NoError(tb, err)
		buf := make([]byte, offset+len(fileContents))
		_, _ = file.Read(buf)
		assert.Equal(tb, append(make([]byte, offset), []byte(fileContents)...), buf)
		assert.NoError(tb, file.Close())
	})
}

func TestFileReadDir(tb testing.TB, o FSOptions) {
	readDirFile := func(tb testing.TB, file hackpadfs.File) hackpadfs.DirReaderFile {
		if file, ok := file.(hackpadfs.DirReaderFile); ok {
			return file
		}
		tb.Skip("File is not a DirReaderFile")
		return nil
	}

	o.tbRun(tb, "list initial root", func(tb testing.TB) {
		_, commit := o.Setup.FS(tb)
		fs := commit()
		file, err := fs.Open(".")
		assert.NoError(tb, err)
		f := readDirFile(tb, file)
		_, err = f.ReadDir(0)
		assert.NoError(tb, err)
		assert.NoError(tb, f.Close())
	})

	o.tbRun(tb, "list root", func(tb testing.TB) {
		setupFS, commit := o.Setup.FS(tb)
		file, err := hackpadfs.Create(setupFS, "foo")
		if assert.NoError(tb, err) {
			assert.NoError(tb, file.Close())
		}
		assert.NoError(tb, hackpadfs.Mkdir(setupFS, "bar", 0700))

		fs := commit()
		file, err = fs.Open(".")
		assert.NoError(tb, err)
		f := readDirFile(tb, file)
		entries, err := f.ReadDir(0)
		assert.NoError(tb, err)
		assert.NoError(tb, f.Close())
		sort.SliceStable(entries, func(a, b int) bool {
			return entries[a].Name() < entries[b].Name()
		})
		o.assertSubsetQuickInfos(tb, []quickInfo{
			{Name: "bar", Mode: hackpadfs.ModeDir | 0700, IsDir: true},
			{Name: "foo", Mode: 0666},
		}, asQuickDirInfos(tb, entries))
	})

	o.tbRun(tb, "readdir batches", func(tb testing.TB) {
		setupFS, commit := o.Setup.FS(tb)
		file, err := hackpadfs.Create(setupFS, "foo")
		if assert.NoError(tb, err) {
			assert.NoError(tb, file.Close())
		}
		assert.NoError(tb, hackpadfs.Mkdir(setupFS, "bar", 0700))

		fs := commit()
		file, err = fs.Open(".")
		assert.NoError(tb, err)
		f := readDirFile(tb, file)
		entries1, err := f.ReadDir(1)
		assert.NoError(tb, err)
		entries2, err := f.ReadDir(1)
		assert.NoError(tb, err)
		assert.NoError(tb, f.Close())

		file, err = fs.Open(".")
		assert.NoError(tb, err)
		f = readDirFile(tb, file)
		entriesAll, err := f.ReadDir(0)
		assert.NoError(tb, err)
		assert.NoError(tb, f.Close())

		var entries []hackpadfs.DirEntry
		entries = append(entries, entries1...)
		entries = append(entries, entries2...)
		assert.Equal(tb, 2, len(entries))
		o.assertSubsetQuickInfos(tb, asQuickDirInfos(tb, entries), asQuickDirInfos(tb, entriesAll))
		o.assertSubsetQuickInfos(tb, []quickInfo{
			{Name: "bar", Mode: hackpadfs.ModeDir | 0700, IsDir: true},
			{Name: "foo", Mode: 0666},
		}, asQuickDirInfos(tb, entriesAll))
	})

	o.tbRun(tb, "readdir high N", func(tb testing.TB) {
		setupFS, commit := o.Setup.FS(tb)
		assert.NoError(tb, hackpadfs.Mkdir(setupFS, "bar", 0700))

		fs := commit()
		file, err := fs.Open(".")
		assert.NoError(tb, err)
		f := readDirFile(tb, file)
		entries, err := f.ReadDir(100000000)
		assert.NoError(tb, err)
		assert.NoError(tb, file.Close())
		o.assertSubsetQuickInfos(tb,
			[]quickInfo{{Name: "bar", Mode: hackpadfs.ModeDir | 0700, IsDir: true}},
			asQuickDirInfos(tb, entries),
		)
	})

	o.tbRun(tb, "list empty subdirectory", func(tb testing.TB) {
		setupFS, commit := o.Setup.FS(tb)
		assert.NoError(tb, hackpadfs.Mkdir(setupFS, "foo", 0700))

		fs := commit()
		file, err := fs.Open("foo")
		assert.NoError(tb, err)
		f := readDirFile(tb, file)
		entries, err := f.ReadDir(0)
		assert.NoError(tb, err)
		assert.NoError(tb, f.Close())
		assert.Equal(tb, 0, len(entries))
	})

	o.tbRun(tb, "list subdirectory", func(tb testing.TB) {
		setupFS, commit := o.Setup.FS(tb)
		assert.NoError(tb, hackpadfs.Mkdir(setupFS, "foo", 0700))
		file, err := hackpadfs.Create(setupFS, "foo/bar")
		if assert.NoError(tb, err) {
			assert.NoError(tb, file.Close())
		}
		file, err = hackpadfs.Create(setupFS, "foo/baz")
		if assert.NoError(tb, err) {
			assert.NoError(tb, file.Close())
		}
		assert.NoError(tb, hackpadfs.Mkdir(setupFS, "foo/boo", 0700))

		fs := commit()
		file, err = fs.Open("foo")
		assert.NoError(tb, err)
		f := readDirFile(tb, file)
		entries, err := f.ReadDir(0)
		assert.NoError(tb, err)
		assert.NoError(tb, f.Close())
		sort.SliceStable(entries, func(a, b int) bool {
			return entries[a].Name() < entries[b].Name()
		})
		o.assertEqualQuickInfos(tb, []quickInfo{
			{Name: "bar", Mode: 0666},
			{Name: "baz", Mode: 0666},
			{Name: "boo", Mode: hackpadfs.ModeDir | 0700, IsDir: true},
		}, asQuickDirInfos(tb, entries))
	})

	o.tbRun(tb, "list on file", func(tb testing.TB) {
		setupFS, commit := o.Setup.FS(tb)
		{
			f, err := hackpadfs.Create(setupFS, "foo")
			if assert.NoError(tb, err) {
				assert.NoError(tb, f.Close())
			}
		}
		fs := commit()
		file, err := fs.Open("foo")
		assert.NoError(tb, err)
		f := readDirFile(tb, file)
		tb.Cleanup(func() { assert.NoError(tb, f.Close()) })
		entries, err := f.ReadDir(0)
		if assert.IsType(tb, &hackpadfs.PathError{}, err) {
			err := err.(*hackpadfs.PathError)
			assert.ErrorIs(tb, hackpadfs.ErrNotDir, err)
			assert.Contains(tb, []string{
				"fdopendir",  // macOS
				"readdirent", // Linux
				"readdir",    // Windows
			}, err.Op)
			assert.Equal(tb, "foo", err.Path)
		}
		assert.Equal(tb, 0, len(entries))
	})
}

func TestFileStat(tb testing.TB, o FSOptions) {
	testStat(tb, o, func(tb testing.TB, fs hackpadfs.FS, path string) (hackpadfs.FileInfo, error) {
		tb.Helper()
		f, err := fs.Open(path)
		if err != nil {
			return nil, err
		}
		tb.Cleanup(func() {
			assert.NoError(tb, f.Close())
		})
		return f.Stat()
	})
}

func TestFileSync(tb testing.TB, o FSOptions) {
	setupFS, commit := o.Setup.FS(tb)
	const fileContents = "hello world"
	file, err := hackpadfs.Create(setupFS, "foo")
	if assert.NoError(tb, err) {
		_, err = hackpadfs.WriteFile(file, []byte(fileContents))
		assert.NoError(tb, err)
		assert.NoError(tb, file.Close())
	}

	fs := commit()
	if openFileFS, ok := fs.(hackpadfs.OpenFileFS); ok {
		// some FSs require write access to perform a sync (Windows), so try to add that access
		file, err = openFileFS.OpenFile("foo", hackpadfs.FlagWriteOnly, 0)
	} else {
		file, err = fs.Open("foo")
	}
	assert.NoError(tb, err)
	f, ok := file.(hackpadfs.SyncerFile)
	if !ok {
		tb.Skip("File is not a SyncerFile")
	}
	_, err = hackpadfs.WriteFile(f, []byte("hello"))
	assert.NoError(tb, err)
	assert.NoError(tb, f.Sync())
	assert.NoError(tb, f.Close())
}

func TestFileTruncate(tb testing.TB, o FSOptions) {
	const fileContents = "hello world"
	for _, tc := range []struct {
		description   string
		size          int64
		expectErrKind error
	}{
		{
			description:   "negative size",
			size:          -1,
			expectErrKind: hackpadfs.ErrInvalid,
		},
		{
			description: "zero size",
			size:        0,
		},
		{
			description: "small size",
			size:        1,
		},
		{
			description: "too big",
			size:        int64(len(fileContents)) * 2,
		},
	} {
		o.tbRun(tb, tc.description, func(tb testing.TB) {
			setupFS, commit := o.Setup.FS(tb)
			file, err := hackpadfs.Create(setupFS, "foo")
			if assert.NoError(tb, err) {
				_, err = hackpadfs.WriteFile(file, []byte(fileContents))
				assert.NoError(tb, err)
				assert.NoError(tb, file.Close())
			}

			fs, ok := commit().(hackpadfs.OpenFileFS)
			if !ok {
				tb.Skip("FS is not an OpenFileFS")
			}
			file, err = hackpadfs.OpenFile(fs, "foo", hackpadfs.FlagReadWrite, 0)
			assert.NoError(tb, err)
			f, ok := file.(hackpadfs.TruncaterFile)
			if !ok {
				tb.Skip("File is not a TruncaterFile")
			}
			err = f.Truncate(tc.size)
			assert.NoError(tb, f.Close())
			if tc.expectErrKind != nil {
				assert.Error(tb, err)
				if assert.IsType(tb, &hackpadfs.PathError{}, err) {
					err := err.(*hackpadfs.PathError)
					assert.Equal(tb, "truncate", err.Op)
					assert.Equal(tb, "foo", err.Path)
					assert.ErrorIs(tb, tc.expectErrKind, err.Err)
				}
				o.tryAssertEqualFS(tb, map[string]fsEntry{
					"foo": {Mode: 0666, Size: int64(len(fileContents))},
				}, fs)
			} else {
				assert.NoError(tb, err)
				o.tryAssertEqualFS(tb, map[string]fsEntry{
					"foo": {Mode: 0666, Size: tc.size},
				}, fs)
			}
		})
	}
}
