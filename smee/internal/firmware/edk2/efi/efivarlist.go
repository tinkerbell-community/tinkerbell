package efi

import (
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
)

// EfiVarList is a map of variable names to EfiVar objects.
type EfiVarList []*EfiVar

// NewEfiVarList creates a new empty EfiVarList.
func NewEfiVarList() EfiVarList {
	return EfiVarList{}
}

func (l EfiVarList) Bytes() []byte {
	blob := []byte{}
	for _, evar := range l {
		blob = append(blob, evar.Bytes()...)
	}
	return blob
}

func (l *EfiVarList) Add(v *EfiVar) error {
	if v == nil {
		return errors.New("cannot add nil EfiVar")
	}
	exists := slices.ContainsFunc(*l, func(s *EfiVar) bool { return s.Name.String() == v.Name.String() })
	if exists {
		return fmt.Errorf("variable %s already exists", v.Name.String())
	}
	*l = append(*l, v)
	log.Printf("added variable: %s", v.Name.String())
	return nil
}

// Create creates a new variable in the list.
func (l *EfiVarList) Create(name string) (*EfiVar, error) {
	log.Printf("create variable %s", name)

	v, err := NewEfiVar(name, nil, 0, []byte{}, 0)
	if err != nil {
		return nil, err
	}

	err = l.Add(v)
	if err != nil {
		return nil, err
	}
	return v, nil
}

// Delete deletes a variable from the list.
func (l *EfiVarList) Delete(name string) {
	index := slices.IndexFunc(*l, func(v *EfiVar) bool { return v.Name.String() == name })
	if index != -1 {
		log.Printf("delete variable: %s", name)
		*l = slices.Delete(*l, index, index+1)
	} else {
		log.Printf("warning: variable %s not found", name)
	}
}

// SetBool sets a boolean variable.
func (l *EfiVarList) SetBool(name string, value bool) error {
	index := slices.IndexFunc(*l, func(v *EfiVar) bool { return v.Name.String() == name })
	var v *EfiVar
	if index == -1 {
		var err error
		v, err = l.Create(name)
		if err != nil {
			return err
		}
	} else {
		v = (*l)[index]
	}

	log.Printf("set variable %s: %v", name, value)
	v.SetBool(value)
	return nil
}

// SetUint32 sets a 32-bit unsigned integer variable.
func (l *EfiVarList) SetUint32(name string, value uint32) error {
	index := slices.IndexFunc(*l, func(v *EfiVar) bool { return v.Name.String() == name })
	var v *EfiVar
	if index == -1 {
		var err error
		v, err = l.Create(name)
		if err != nil {
			return err
		}
	} else {
		v = (*l)[index]
	}

	log.Printf("set variable %s: %d", name, value)
	v.SetUint32(value)
	return nil
}

// SetBootEntry sets a boot entry variable.
func (l *EfiVarList) SetBootEntry(index uint16, title string, path string, optdata []byte) error {
	name := fmt.Sprintf("Boot%04X", index)
	varIndex := slices.IndexFunc(*l, func(v *EfiVar) bool { return v.Name.String() == name })
	var v *EfiVar
	if varIndex == -1 {
		var err error
		v, err = l.Create(name)
		if err != nil {
			return err
		}
	} else {
		v = (*l)[varIndex]
	}

	log.Printf("set variable %s: %s = %s", name, title, path)
	return v.SetBootEntry(LOAD_OPTION_ACTIVE, title, path, optdata)
}

// AddBootEntry adds a new boot entry and returns its index.
func (l *EfiVarList) AddBootEntry(title string, path string, optdata []byte) (uint16, error) {
	for index := uint16(0); index < 0xffff; index++ {
		name := fmt.Sprintf("Boot%04X", index)
		exists := slices.ContainsFunc(*l, func(v *EfiVar) bool { return v.Name.String() == name })
		if !exists {
			err := l.SetBootEntry(index, title, path, optdata)
			if err != nil {
				return 0, err
			}
			return index, nil
		}
	}

	return 0, errors.New("no free boot entry slots")
}

func (l *EfiVarList) GetBootNext() (uint16, error) {
	index := slices.IndexFunc(*l, func(v *EfiVar) bool { return v.Name.String() == BootNext })
	if index == -1 {
		return 0, errors.New("BootNext variable not found")
	}
	v := (*l)[index]
	return v.GetBootNext()
}

// SetBootNext sets the BootNext variable.
func (l *EfiVarList) SetBootNext(index uint16) error {
	varIndex := slices.IndexFunc(*l, func(v *EfiVar) bool { return v.Name.String() == BootNext })
	var v *EfiVar
	if varIndex == -1 {
		var err error
		v, err = l.Create(BootNext)
		if err != nil {
			return err
		}
	} else {
		v = (*l)[varIndex]
	}

	log.Printf("set variable BootNext: 0x%04X", index)
	v.SetBootNext(index)
	return nil
}

// SetBootOrder sets the BootOrder variable.
func (l *EfiVarList) SetBootOrder(order []uint16) error {
	index := slices.IndexFunc(*l, func(v *EfiVar) bool { return v.Name.String() == "BootOrder" })
	var v *EfiVar
	if index == -1 {
		var err error
		v, err = l.Create("BootOrder")
		if err != nil {
			return err
		}
	} else {
		v = (*l)[index]
	}

	log.Printf("set variable BootOrder: %v", order)
	v.SetBootOrder(order)
	return nil
}

// AppendBootOrder appends to the BootOrder variable.
func (l *EfiVarList) AppendBootOrder(index uint16) error {
	varIndex := slices.IndexFunc(*l, func(v *EfiVar) bool { return v.Name.String() == "BootOrder" })
	var v *EfiVar
	if varIndex == -1 {
		var err error
		v, err = l.Create("BootOrder")
		if err != nil {
			return err
		}
	} else {
		v = (*l)[varIndex]
	}

	log.Printf("append to variable BootOrder: 0x%04X", index)
	v.AppendBootOrder(index)
	return nil
}

// GetBootOrder retrieves the BootOrder variable.
func (l *EfiVarList) GetBootOrder() ([]uint16, error) {
	index := slices.IndexFunc(*l, func(v *EfiVar) bool { return v.Name.String() == "BootOrder" })
	if index == -1 {
		return nil, errors.New("BootOrder variable not found")
	}
	v := (*l)[index]

	return v.GetBootOrder()
}

// SetFromFile sets a variable's data from a file.
func (l *EfiVarList) SetFromFile(name string, filename string) error {
	index := slices.IndexFunc(*l, func(v *EfiVar) bool { return v.Name.String() == name })
	var v *EfiVar
	if index == -1 {
		var err error
		v, err = l.Create(name)
		if err != nil {
			return err
		}
	} else {
		v = (*l)[index]
	}

	log.Printf("set variable %s from file %s", name, filename)
	return v.SetFromFile(filename)
}

// GetBootEntry retrieves a boot entry.
func (l *EfiVarList) GetBootEntry(index uint16) (*BootEntry, error) {
	name := fmt.Sprintf("Boot%04X", index)
	varIndex := slices.IndexFunc(*l, func(v *EfiVar) bool { return v.Name.String() == name })
	if varIndex == -1 {
		return nil, errors.New("boot entry not found")
	}
	v := (*l)[varIndex]

	return v.GetBootEntry()
}

// ListBootEntries lists all boot entries.
func (l *EfiVarList) ListBootEntries() (map[uint16]*BootEntry, error) {
	entries := make(map[uint16]*BootEntry)

	for index := uint16(0); index < 0xffff; index++ {
		name := fmt.Sprintf("Boot%04X", index)
		varIndex := slices.IndexFunc(*l, func(v *EfiVar) bool { return v.Name.String() == name })
		if varIndex == -1 {
			continue
		}
		v := (*l)[varIndex]

		entry, err := v.GetBootEntry()
		if err != nil {
			return nil, err
		}

		entries[index] = entry
	}

	return entries, nil
}

// DeleteBootEntry deletes a boot entry.
func (l *EfiVarList) DeleteBootEntry(index uint16) error {
	name := fmt.Sprintf("Boot%04X", index)
	varIndex := slices.IndexFunc(*l, func(v *EfiVar) bool { return v.Name.String() == name })
	if varIndex == -1 {
		return errors.New("boot entry not found")
	}

	log.Printf("delete variable %s", name)
	l.Delete(name)
	return nil
}

// FindFirst returns the first variable that matches the criteria.
func (l *EfiVarList) FindFirst(predicate func(name string, efiVar *EfiVar) bool) (*EfiVar, string) {
	for _, v := range *l {
		name := v.Name.String()
		if predicate(name, v) {
			return v, name
		}
	}
	return nil, ""
}

// Variables returns the variables in the list.
func (l *EfiVarList) Variables() []*EfiVar {
	vars := make([]*EfiVar, len(*l))
	copy(vars, *l)
	return vars
}

// FindByPrefix returns all variables that have names starting with the given prefix.
func (l *EfiVarList) FindByPrefix(prefix string) []*EfiVar {
	vars := make([]*EfiVar, 0)
	for _, v := range *l {
		if strings.HasPrefix(v.Name.String(), prefix) {
			vars = append(vars, v)
		}
	}
	return vars
}
