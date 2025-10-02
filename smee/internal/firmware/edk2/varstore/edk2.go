package varstore

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"slices"

	"github.com/go-logr/logr"
	"github.com/tinkerbell/tinkerbell/smee/internal/firmware/edk2/efi"
)

type Edk2VarStore struct {
	data  []byte
	start int
	end   int

	Logger logr.Logger
}

func New(data []byte) (*Edk2VarStore, error) {
	vs := &Edk2VarStore{
		data: data,
	}
	if err := vs.parseVolume(); err != nil {
		return nil, err
	}
	return vs, nil
}

func (vs *Edk2VarStore) GetVarList() (efi.EfiVarList, error) {
	pos := vs.start
	varlist := efi.EfiVarList{}
	for pos < vs.end {
		magic := binary.LittleEndian.Uint16(vs.data[pos:])
		if magic != 0x55aa {
			break
		}
		state := vs.data[pos+2]
		attr := binary.LittleEndian.Uint32(vs.data[pos+4:])
		count := binary.LittleEndian.Uint64(vs.data[pos+8:])

		pk := binary.LittleEndian.Uint32(vs.data[pos+32:])
		nsize := binary.LittleEndian.Uint32(vs.data[pos+36:])
		dsize := binary.LittleEndian.Uint32(vs.data[pos+40:])

		if state == 0x3f {
			varName := efi.FromUCS16(vs.data[pos+44+16:])
			varData := vs.data[uint32(pos)+44+16+nsize : uint32(pos)+44+16+nsize+dsize]
			varItem := efi.EfiVar{
				Name:  varName,
				Guid:  efi.ParseBinGUID(vs.data, pos+44),
				Attr:  attr,
				Data:  varData,
				Count: int(count),
				PkIdx: int(pk),
			}
			_ = varItem.ParseTime(vs.data, pos+16)
			idx := slices.IndexFunc(varlist, func(v *efi.EfiVar) bool { return v.Name.String() == varItem.Name.String() && v.Guid == varItem.Guid })
			if idx != -1 {
				// Update existing variable
				varlist[idx] = &varItem
			} else {
				// Add new variable
				varlist = append(varlist, &varItem)
			}
			_ = varItem.ParseTime(vs.data, pos+16)
		}

		pos += 44 + 16 + int(nsize) + int(dsize)
		pos = (pos + 3) & ^3 // align
	}
	return varlist, nil
}

func (vs *Edk2VarStore) ReadBytes(varlist efi.EfiVarList) (io.Reader, error) {
	blob, err := vs.bytesVarStore(varlist)
	if err != nil {
		vs.Logger.Error(err, "failed to convert varlist to bytes")
		return nil, err
	}
	return bytes.NewReader(blob), nil
}

func (vs *Edk2VarStore) ReadAll(varlist efi.EfiVarList) ([]byte, error) {
	blob, err := vs.bytesVarStore(varlist)
	if err != nil {
		vs.Logger.Error(err, "failed to convert varlist to bytes")
		return nil, err
	}
	return blob, nil
}

func (vs *Edk2VarStore) findNvData(data []byte) int {
	offset := 0
	for offset+64 < len(data) {
		guid := efi.ParseBinGUID(data, offset+16)
		if guid.String() == efi.NvData {
			return offset
		}
		if guid.String() == efi.Ffs {
			tlen := binary.LittleEndian.Uint64(data[offset+32 : offset+40])
			offset += int(tlen)
			continue
		}
		offset += 1024
	}
	return -1
}

func (e *Edk2VarStore) parseVolume() error {
	offset := e.findNvData(e.data)
	if offset < 1 {
		return fmt.Errorf("varstore not found")
	}

	guid := efi.ParseBinGUID(e.data, offset+16)

	// Equivalent to struct.unpack_from("=QLLHHHxBLL", self.filedata, offset + 32)
	r := bytes.NewReader(e.data[offset+32:])

	var vlen uint64
	var sig, attr uint32
	var hlen, csum, xoff uint16
	var rev uint8
	var blocks, blksize uint32

	if err := binary.Read(r, binary.LittleEndian, &vlen); err != nil {
		return fmt.Errorf("failed to read vlen: %w", err)
	}
	if err := binary.Read(r, binary.LittleEndian, &sig); err != nil {
		return fmt.Errorf("failed to read sig: %w", err)
	}
	if err := binary.Read(r, binary.LittleEndian, &attr); err != nil {
		return fmt.Errorf("failed to read attr: %w", err)
	}
	if err := binary.Read(r, binary.LittleEndian, &hlen); err != nil {
		return fmt.Errorf("failed to read hlen: %w", err)
	}
	if err := binary.Read(r, binary.LittleEndian, &csum); err != nil {
		return fmt.Errorf("failed to read csum: %w", err)
	}
	if err := binary.Read(r, binary.LittleEndian, &xoff); err != nil {
		return fmt.Errorf("failed to read xoff: %w", err)
	}

	// Skip the pad byte (equivalent to 'x' in struct format)
	if _, err := r.Seek(1, io.SeekCurrent); err != nil {
		return fmt.Errorf("failed to skip pad byte: %w", err)
	}

	if err := binary.Read(r, binary.LittleEndian, &rev); err != nil {
		return fmt.Errorf("failed to read rev: %w", err)
	}
	if err := binary.Read(r, binary.LittleEndian, &blocks); err != nil {
		return fmt.Errorf("failed to read blocks: %w", err)
	}
	if err := binary.Read(r, binary.LittleEndian, &blksize); err != nil {
		return fmt.Errorf("failed to read blksize: %w", err)
	}

	e.Logger.Info("vol=%s vlen=0x%x rev=%d blocks=%d*%d (0x%x)",
		efi.GuidName(guid), vlen, rev, blocks, blksize, blocks*blksize)

	if sig != 0x4856465f {
		err := fmt.Errorf("invalid signature: 0x%x", sig)
		e.Logger.Error(err, "sig", sig)
		return err
	}

	if guid.String() != efi.NvData {
		err := fmt.Errorf("not a volume: %s", guid)
		e.Logger.Error(err, "guid", guid)
		return err
	}

	return e.parseVarstore(offset + int(hlen))
}

func (vs *Edk2VarStore) parseVarstore(start int) error {
	guid := efi.ParseBinGUID(vs.data, start)
	size := binary.LittleEndian.Uint32(vs.data[start+16 : start+20])
	storefmt := vs.data[start+20]
	state := vs.data[start+21]

	vs.Logger.Info("varstore=%s size=0x%x format=0x%x state=0x%x",
		efi.GuidName(guid), size, storefmt, state)

	if guid.String() != efi.AuthVars {
		return fmt.Errorf("unknown varstore guid: %s", guid)
	}
	if storefmt != 0x5a {
		return fmt.Errorf("unknown varstore format: 0x%x", storefmt)
	}
	if state != 0xfe {
		return fmt.Errorf("unknown varstore state: 0x%x", state)
	}

	vs.start = start + 16 + 12
	vs.end = start + int(size)
	vs.Logger.Info("var store range: 0x%x -> 0x%x", vs.start, vs.end)
	return nil
}

func (vs *Edk2VarStore) bytesVarList(varlist efi.EfiVarList) ([]byte, error) {
	blob := varlist.Bytes()
	mlen := vs.end - vs.start
	if len(blob) > mlen {
		err := fmt.Errorf("varstore is too small: %d > %d", len(blob), mlen)
		vs.Logger.Error(err, "size", len(blob), "max", mlen)
		return nil, err
	}
	return blob, nil
}

func (vs *Edk2VarStore) bytesVarStore(varlist efi.EfiVarList) ([]byte, error) {
	blob := slices.Clone(vs.data[:vs.start])

	// Append the variable list
	newVarList, err := vs.bytesVarList(varlist)
	if err != nil {
		vs.Logger.Error(err, "failed to convert varlist to bytes")
		return nil, err
	}

	blob = append(blob, newVarList...)
	for len(blob) < vs.end {
		blob = append(blob, 0xff)
	}
	blob = append(blob, vs.data[vs.end:]...)
	return blob, nil
}
