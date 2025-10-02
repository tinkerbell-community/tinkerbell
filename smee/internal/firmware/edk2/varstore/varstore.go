package varstore

import "github.com/tinkerbell/tinkerbell/smee/internal/firmware/edk2/efi"

type VarStore interface {
	GetVarList() (efi.EfiVarList, error)
	WriteVarStore(filename string, varlist efi.EfiVarList) error
}
