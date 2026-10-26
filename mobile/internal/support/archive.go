package support

import (
	"archive/zip"
	"errors"
	"io"
)

// Archive is the common mobile support format. Callers supply fixed log names.
func Archive(w io.Writer, diagnostics []byte, logs map[string]string, metadata []byte, readme string, names []string) error {
	report, err := Report(diagnostics, logs)
	if err != nil {
		return err
	}
	z := zip.NewWriter(w)
	write := func(name string, data []byte) error {
		f, e := z.Create(name)
		if e != nil {
			return e
		}
		_, e = f.Write(data)
		return e
	}
	if err = write("support.json", report); err == nil && len(metadata) > 0 {
		err = write("snapshot.json", metadata)
	}
	if err == nil {
		err = write("README.txt", []byte(readme))
	}
	for _, name := range names {
		if text, ok := logs[name]; ok && err == nil {
			err = write("logs/"+name, FilteredLog(text))
		}
	}
	return errors.Join(err, z.Close())
}
