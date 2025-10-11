package binary

import (
	"bytes"
	"text/template"
)

func GenerateTemplate(d any) ([]byte, error) {
	t := template.New("pxelinux.cfg")
	t, err := t.Parse(PxeTemplate)
	if err != nil {
		return []byte{}, err
	}
	buffer := new(bytes.Buffer)
	if err := t.Execute(buffer, d); err != nil {
		return []byte{}, err
	}

	return buffer.Bytes(), nil
}
