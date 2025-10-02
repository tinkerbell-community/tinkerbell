package varstore

import (
	"reflect"
	"testing"

	"github.com/go-logr/logr"
	"github.com/tinkerbell/tinkerbell/smee/internal/firmware/edk2/efi"
)

func TestEdk2VarStore_GetVarList(t *testing.T) {
	type fields struct {
		filedata []byte
		start    int
		end      int
		Logger   logr.Logger
	}
	tests := []struct {
		name    string
		fields  fields
		want    efi.EfiVarList
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := &Edk2VarStore{
				data:   tt.fields.filedata,
				start:  tt.fields.start,
				end:    tt.fields.end,
				Logger: tt.fields.Logger,
			}
			got, err := vs.GetVarList()
			if (err != nil) != tt.wantErr {
				t.Errorf("Edk2VarStore.GetVarList() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Edk2VarStore.GetVarList() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEdk2VarStore_findNvData(t *testing.T) {
	type fields struct {
		filedata []byte
		start    int
		end      int
		Logger   logr.Logger
	}
	type args struct {
		data []byte
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   int
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := &Edk2VarStore{
				data:   tt.fields.filedata,
				start:  tt.fields.start,
				end:    tt.fields.end,
				Logger: tt.fields.Logger,
			}
			if got := vs.findNvData(tt.args.data); got != tt.want {
				t.Errorf("Edk2VarStore.findNvData() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEdk2VarStore_parseVolume(t *testing.T) {
	type fields struct {
		filedata []byte
		start    int
		end      int
		Logger   logr.Logger
	}
	tests := []struct {
		name    string
		fields  fields
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &Edk2VarStore{
				data:   tt.fields.filedata,
				start:  tt.fields.start,
				end:    tt.fields.end,
				Logger: tt.fields.Logger,
			}
			if err := e.parseVolume(); (err != nil) != tt.wantErr {
				t.Errorf("Edk2VarStore.parseVolume() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEdk2VarStore_parseVarstore(t *testing.T) {
	type fields struct {
		filedata []byte
		start    int
		end      int
		Logger   logr.Logger
	}
	type args struct {
		start int
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := &Edk2VarStore{
				data:   tt.fields.filedata,
				start:  tt.fields.start,
				end:    tt.fields.end,
				Logger: tt.fields.Logger,
			}
			if err := vs.parseVarstore(tt.args.start); (err != nil) != tt.wantErr {
				t.Errorf("Edk2VarStore.parseVarstore() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestEdk2VarStore_bytesVarList(t *testing.T) {
	type fields struct {
		filedata []byte
		start    int
		end      int
		Logger   logr.Logger
	}
	type args struct {
		varlist efi.EfiVarList
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    []byte
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := &Edk2VarStore{
				data:   tt.fields.filedata,
				start:  tt.fields.start,
				end:    tt.fields.end,
				Logger: tt.fields.Logger,
			}
			got, err := vs.bytesVarList(tt.args.varlist)
			if (err != nil) != tt.wantErr {
				t.Errorf("Edk2VarStore.bytesVarList() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Edk2VarStore.bytesVarList() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEdk2VarStore_bytesVarStore(t *testing.T) {
	type fields struct {
		filedata []byte
		start    int
		end      int
		Logger   logr.Logger
	}
	type args struct {
		varlist efi.EfiVarList
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		want    []byte
		wantErr bool
	}{
		// TODO: Add test cases.
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vs := &Edk2VarStore{
				data:   tt.fields.filedata,
				start:  tt.fields.start,
				end:    tt.fields.end,
				Logger: tt.fields.Logger,
			}
			got, err := vs.bytesVarStore(tt.args.varlist)
			if (err != nil) != tt.wantErr {
				t.Errorf("Edk2VarStore.bytesVarStore() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Edk2VarStore.bytesVarStore() = %v, want %v", got, tt.want)
			}
		})
	}
}
