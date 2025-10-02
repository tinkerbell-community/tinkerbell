package varstore_test

import (
	"github.com/stretchr/testify/assert"
	"github.com/tinkerbell/tinkerbell/smee/internal/firmware/edk2/efi"
)

// MockVarStore implements the VarStore interface for testing.
type MockVarStore struct {
	varList     efi.EfiVarList
	writeErrors bool
}

func NewMockVarStore(writeErrors bool) *MockVarStore {
	return &MockVarStore{
		varList:     efi.NewEfiVarList(),
		writeErrors: writeErrors,
	}
}

func (m *MockVarStore) GetVarList() (efi.EfiVarList, error) {
	return m.varList, nil
}

func (m *MockVarStore) WriteVarStore(filename string, varlist efi.EfiVarList) error {
	if m.writeErrors {
		return assert.AnError
	}
	m.varList = varlist
	return nil
}
