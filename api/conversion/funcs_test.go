package conversion

import (
	"text/template"

	"github.com/Masterminds/sprig/v3"
)

// testFuncs mirrors the template functions Tinkerbell gives Templates
// (pkg/template/funcmap, which this module cannot import): sprig's hermetic
// functions plus Tinkerbell's own, stubbed here since the tests only parse
// templates with them or render sprig functions.
func testFuncs() template.FuncMap {
	fm := sprig.HermeticTxtFuncMap()
	for _, name := range []string{"formatPartition", "netmaskToPrefixLength", "toYaml", "fromYaml"} {
		fm[name] = func(...any) string { return "" }
	}
	return fm
}
