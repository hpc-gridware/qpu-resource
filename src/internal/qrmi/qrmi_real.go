// Copyright 2026 Pasqal and its contributors
// SPDX-License-Identifier: Apache-2.0

//go:build qrmi

package qrmi

// #cgo LDFLAGS: -lqrmi -Wl,-rpath,$ORIGIN
// #include <stdlib.h>
// #include <stdbool.h>
// #include "qrmi.h"
// #if defined(QRMI_VERSION) && QRMI_VERSION >= QRMI_VERSION_NUMERIC(0,18,0)
// extern void qrmiGoLogCallback(char *level, char *target, char *message);
// static inline void qrmi_set_go_log_callback(void) {
//     qrmi_log_callback_set((QrmiLogCallback)qrmiGoLogCallback);
// }
// #else
// static inline void qrmi_set_go_log_callback(void) {}
// #endif
import "C"

import (
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

var (
	logMu   sync.Mutex
	logFunc LogFunc
)

// SetLogCallback routes QRMI Rust logs through fn.
func SetLogCallback(fn LogFunc) {
	logMu.Lock()
	logFunc = fn
	logMu.Unlock()
	C.qrmi_set_go_log_callback()
}

//export qrmiGoLogCallback
func qrmiGoLogCallback(level, target, message *C.char) {
	logMu.Lock()
	fn := logFunc
	logMu.Unlock()
	if fn != nil {
		fn(goStringOrEmpty(level), goStringOrEmpty(target), goStringOrEmpty(message))
	}
}

// Config wraps QrmiConfig from libqrmi.
type Config struct {
	c *C.QrmiConfig
}

// LoadConfig loads a qrmi_config.json file. The returned Config must be
// released with Close to free the underlying C allocation.
func LoadConfig(path string) (*Config, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	cfg := C.qrmi_config_load(cpath)
	if cfg == nil {
		return nil, fmt.Errorf("qrmi_config_load(%s): %s", path, lastError())
	}
	return &Config{c: cfg}, nil
}

// Close releases the underlying C config. Safe to call on a nil receiver.
func (c *Config) Close() {
	if c == nil || c.c == nil {
		return
	}
	C.qrmi_config_free(c.c)
	c.c = nil
}

// ResourceDef looks up a resource definition by name from the loaded config.
// The returned ResourceDef must be released with Close.
func (c *Config) ResourceDef(name string) (*ResourceDef, error) {
	if c == nil || c.c == nil {
		return nil, errors.New("qrmi: config is closed")
	}
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	def := C.qrmi_config_resource_def_get(c.c, cname)
	if def == nil {
		return nil, fmt.Errorf("qrmi_config_resource_def_get(%s): %s", name, lastError())
	}
	return &ResourceDef{c: def}, nil
}

// ResourceDef wraps QrmiResourceDef from libqrmi.
type ResourceDef struct {
	c *C.QrmiResourceDef
}

// Close releases the underlying C resource definition. Safe to call on a
// nil receiver.
func (d *ResourceDef) Close() {
	if d == nil || d.c == nil {
		return
	}
	C.qrmi_config_resource_def_free(d.c)
	d.c = nil
}

// Type returns the QRMI resource type as the integer the C API exposes.
// Callers pass this back into NewResource and emit it into the metadata
// TSV; the integer value is part of the on-disk contract.
func (d *ResourceDef) Type() int {
	if d == nil || d.c == nil {
		return 0
	}
	return int(d.c._type)
}

// TypeString returns the human-readable name of the resource type
// (for example "pasqal-cloud" or "ibm-quantum"). The value is owned by
// libqrmi and must be freed via qrmi_string_free; this method copies it
// into a Go string before freeing.
func (d *ResourceDef) TypeString() string {
	if d == nil || d.c == nil {
		return ""
	}
	cstr := C.qrmi_config_resource_type_to_str(d.c._type)
	if cstr == nil {
		return ""
	}
	out := C.GoString(cstr)
	C.qrmi_string_free((*C.char)(unsafe.Pointer(cstr)))
	return out
}

// Environments returns the configured environment variables for this
// resource. Each entry is a key/value pair; the prolog applies them with
// a backend-prefixed name (for example EMU_FREE_QRMI_PASQAL_CLOUD_AUTH_ENDPOINT).
func (d *ResourceDef) Environments() []EnvVar {
	if d == nil || d.c == nil {
		return nil
	}
	env := d.c.environments
	count := int(env.length)
	if count == 0 || env.variables == nil {
		return nil
	}
	out := make([]EnvVar, 0, count)
	// Treat env.variables as a flat C array of length count.
	base := unsafe.Pointer(env.variables)
	stride := unsafe.Sizeof(C.QrmiKeyValue{})
	for i := 0; i < count; i++ {
		kv := (*C.QrmiKeyValue)(unsafe.Pointer(uintptr(base) + uintptr(i)*stride))
		key := goStringOrEmpty(kv.key)
		val := goStringOrEmpty(kv.value)
		if key == "" {
			continue
		}
		out = append(out, EnvVar{Key: key, Value: val})
	}
	return out
}

// Resource wraps QrmiQuantumResource from libqrmi.
type Resource struct {
	c *C.QrmiQuantumResource
}

// NewResource creates a new QRMI quantum resource handle for the given
// backend name and type. The returned Resource must be released with Close.
func NewResource(name string, typ int) (*Resource, error) {
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))

	r := C.qrmi_resource_new(cname, C.QrmiResourceType(typ))
	if r == nil {
		return nil, fmt.Errorf("qrmi_resource_new(%s): %s", name, lastError())
	}
	return &Resource{c: r}, nil
}

// Close releases the underlying C resource. Safe to call on a nil receiver.
func (r *Resource) Close() {
	if r == nil || r.c == nil {
		return
	}
	C.qrmi_resource_free(r.c)
	r.c = nil
}

// IsAccessible asks the QRMI runtime whether the backend is currently
// reachable. A false return without an error means the backend responded
// but is not currently usable (for example offline, or out of quota).
func (r *Resource) IsAccessible() (bool, error) {
	if r == nil || r.c == nil {
		return false, errors.New("qrmi: resource is closed")
	}
	var out C.bool
	rc := C.qrmi_resource_is_accessible(r.c, &out)
	if rc != C.QRMI_RETURN_CODE_SUCCESS {
		return false, fmt.Errorf("qrmi_resource_is_accessible: %s", lastError())
	}
	return bool(out), nil
}

// Acquire obtains a fresh acquisition token from the backend. The returned
// token is a Go-owned string; the underlying C allocation is freed before
// return.
func (r *Resource) Acquire() (string, error) {
	if r == nil || r.c == nil {
		return "", errors.New("qrmi: resource is closed")
	}
	var token *C.char
	rc := C.qrmi_resource_acquire(r.c, &token)
	if rc != C.QRMI_RETURN_CODE_SUCCESS || token == nil {
		return "", fmt.Errorf("qrmi_resource_acquire: %s", lastError())
	}
	out := C.GoString(token)
	C.qrmi_string_free(token)
	return out, nil
}

// Release returns the given acquisition token to the backend.
func (r *Resource) Release(token string) error {
	if r == nil || r.c == nil {
		return errors.New("qrmi: resource is closed")
	}
	ctok := C.CString(token)
	defer C.free(unsafe.Pointer(ctok))

	rc := C.qrmi_resource_release(r.c, ctok)
	if rc != C.QRMI_RETURN_CODE_SUCCESS {
		return fmt.Errorf("qrmi_resource_release: %s", lastError())
	}
	return nil
}

// lastError reads the QRMI thread-local error string. The C string is owned
// by libqrmi and not freed by the caller per the QRMI C API contract.
func lastError() string {
	cstr := C.qrmi_get_last_error()
	if cstr == nil {
		return "(no error message)"
	}
	return C.GoString(cstr)
}

// goStringOrEmpty converts a possibly-NULL C string into a Go string,
// returning the empty string when the input is NULL.
func goStringOrEmpty(c *C.char) string {
	if c == nil {
		return ""
	}
	return C.GoString(c)
}
