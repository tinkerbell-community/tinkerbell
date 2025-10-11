package firmware

import (
	"embed"
	"io"
	"path/filepath"
)

// content contains the embedded U-Boot firmware files.
//
//go:embed broadcom/*.dtb overlays/*.dtbo firmware/brcm/* *.dat *.elf
//go:embed u-boot.bin config.txt cmdline.txt bootcfg.txt
var content embed.FS

// filenameLookup is a map of base filenames to their full paths in the embedded filesystem.
var filenameLookup = fileMap(content)

// fileMap converts an embed.FS to a map of filenames to embed.File objects.
func fileMap(f embed.FS) map[string]string {
	files := make(map[string]string)

	dirEntries, err := f.ReadDir(".")
	if err != nil {
		return files
	}

	for _, entry := range dirEntries {
		if entry.IsDir() {
			subEntries, err := f.ReadDir(entry.Name())
			if err != nil {
				continue
			}
			for _, subEntry := range subEntries {
				files[filepath.Base(subEntry.Name())] = filepath.Join(entry.Name(), subEntry.Name())
			}
		} else {
			files[filepath.Base(entry.Name())] = entry.Name()
		}
	}

	return files
}

// HandleRead reads the specified filename from the embedded filesystem and writes it
func HandleRead(filename string, rf io.ReaderFrom) error {
	filen, ok := filenameLookup[filepath.Base(filename)]
	if !ok {
		return nil
	}

	f, err := content.Open(filen)
	if err != nil {
		return err
	}
	defer f.Close()

	if info, err := f.Stat(); err != nil {
		return err
	} else {
		if transfer, ok := rf.(interface{ SetSize(int64) }); ok {
			transfer.SetSize(info.Size())
		}
	}

	_, err = rf.ReadFrom(f)
	if err != nil {
		return err
	}

	return nil
}

func ReadAll(filename string) ([]byte, error) {
	filen, ok := filenameLookup[filepath.Base(filename)]
	if !ok {
		return nil, nil
	}

	return content.ReadFile(filen)
}
