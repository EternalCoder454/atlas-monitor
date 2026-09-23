//go:build linux && cgo

package gpu

/*
#cgo LDFLAGS: -ldl

#include <dlfcn.h>
#include <stdlib.h>
#include <string.h>

// NVML is opened with dlopen at runtime, so nothing here is a build-time
// dependency: a machine with no NVIDIA driver simply fails the open and Atlas
// falls through to the generic DRM backend. Only the handful of entry points we
// actually use are resolved, and the two structs below mirror NVML's ABI.

typedef void *nvmlDevice_t;
typedef struct { unsigned int gpu, memory; } atlas_util;
typedef struct { unsigned long long total, free, used; } atlas_mem;

typedef int (*fn_v)(void);
typedef int (*fn_handle)(unsigned int, nvmlDevice_t *);
typedef int (*fn_name)(nvmlDevice_t, char *, unsigned int);
typedef int (*fn_util)(nvmlDevice_t, atlas_util *);
typedef int (*fn_mem)(nvmlDevice_t, atlas_mem *);
typedef int (*fn_u)(nvmlDevice_t, unsigned int *);
typedef int (*fn_enum_u)(nvmlDevice_t, int, unsigned int *);

typedef struct {
    double usage, mem_used, mem_total, temp, fan_pct, power_w, sclk, mclk;
} atlas_gpu_sample;

static void *lib;
static nvmlDevice_t dev;
static fn_v p_shutdown;
static fn_name p_name;
static fn_util p_util;
static fn_mem p_mem;
static fn_enum_u p_temp;
static fn_u p_fan;
static fn_u p_power;
static fn_enum_u p_clock;

// atlas_nvml_open loads NVML and grabs device 0. Returns 1 on success.
static int atlas_nvml_open(void) {
    if (lib) return 1;
    lib = dlopen("libnvidia-ml.so.1", RTLD_LAZY | RTLD_LOCAL);
    if (!lib) lib = dlopen("libnvidia-ml.so", RTLD_LAZY | RTLD_LOCAL);
    if (!lib) return 0;

    fn_v init = (fn_v)dlsym(lib, "nvmlInit_v2");
    if (!init) init = (fn_v)dlsym(lib, "nvmlInit");
    fn_handle handle = (fn_handle)dlsym(lib, "nvmlDeviceGetHandleByIndex_v2");
    if (!handle) handle = (fn_handle)dlsym(lib, "nvmlDeviceGetHandleByIndex");
    p_shutdown = (fn_v)dlsym(lib, "nvmlShutdown");
    if (!init || !handle) { dlclose(lib); lib = NULL; return 0; }

    if (init() != 0) { dlclose(lib); lib = NULL; return 0; }
    if (handle(0, &dev) != 0) {
        if (p_shutdown) p_shutdown();
        dlclose(lib); lib = NULL; return 0;
    }

    // Optional entry points: a missing one just leaves its field at zero.
    p_name  = (fn_name)dlsym(lib, "nvmlDeviceGetName");
    p_util  = (fn_util)dlsym(lib, "nvmlDeviceGetUtilizationRates");
    p_mem   = (fn_mem)dlsym(lib, "nvmlDeviceGetMemoryInfo");
    p_temp  = (fn_enum_u)dlsym(lib, "nvmlDeviceGetTemperature");
    p_fan   = (fn_u)dlsym(lib, "nvmlDeviceGetFanSpeed");
    p_power = (fn_u)dlsym(lib, "nvmlDeviceGetPowerUsage");
    p_clock = (fn_enum_u)dlsym(lib, "nvmlDeviceGetClockInfo");
    return 1;
}

static void atlas_nvml_close(void) {
    if (!lib) return;
    if (p_shutdown) p_shutdown();
    dlclose(lib);
    lib = NULL;
}

// atlas_nvml_name writes the adapter name into buf.
static int atlas_nvml_name(char *buf, unsigned int n) {
    if (!lib || !p_name) return 0;
    return p_name(dev, buf, n) == 0;
}

static void atlas_nvml_sample(atlas_gpu_sample *out) {
    memset(out, 0, sizeof(*out));
    if (!lib) return;

    atlas_util u;
    if (p_util && p_util(dev, &u) == 0) out->usage = (double)u.gpu;

    atlas_mem m;
    if (p_mem && p_mem(dev, &m) == 0) {
        out->mem_used = (double)m.used;
        out->mem_total = (double)m.total;
    }

    unsigned int v;
    if (p_temp && p_temp(dev, 0, &v) == 0) out->temp = (double)v;      // 0 = NVML_TEMPERATURE_GPU
    if (p_fan && p_fan(dev, &v) == 0) out->fan_pct = (double)v;        // percent of maximum
    if (p_power && p_power(dev, &v) == 0) out->power_w = (double)v / 1000.0; // milliwatts
    if (p_clock && p_clock(dev, 0, &v) == 0) out->sclk = (double)v;    // 0 = NVML_CLOCK_GRAPHICS
    if (p_clock && p_clock(dev, 2, &v) == 0) out->mclk = (double)v;    // 2 = NVML_CLOCK_MEM
}
*/
import "C"

import "unsafe"

// nvmlBackend reads an NVIDIA card through NVML. It gives the same depth of
// data as the AMD sysfs backend — utilisation, VRAM, clocks, power, fan — for a
// vendor that exposes almost nothing through sysfs.
type nvmlBackend struct{ name string }

func newNVMLBackend() backend {
	if C.atlas_nvml_open() == 0 {
		return nil
	}
	name := "NVIDIA GPU"
	var buf [96]C.char
	if C.atlas_nvml_name(&buf[0], C.uint(len(buf))) != 0 {
		if n := C.GoString(&buf[0]); n != "" {
			name = n
		}
	}
	return &nvmlBackend{name: name}
}

func (b *nvmlBackend) label() string { return b.name }
func (b *nvmlBackend) close()        { C.atlas_nvml_close() }

func (b *nvmlBackend) sample(s *Sample) {
	var out C.atlas_gpu_sample
	C.atlas_nvml_sample((*C.atlas_gpu_sample)(unsafe.Pointer(&out)))
	s.UsagePct = clampPct(float64(out.usage))
	s.VramUsed = uint64(out.mem_used)
	s.VramTotal = uint64(out.mem_total)
	s.TempC = float64(out.temp)
	s.FanPercent = float64(out.fan_pct)
	s.PowerW = float64(out.power_w)
	s.SclkMHz = float64(out.sclk)
	s.MclkMHz = float64(out.mclk)
}
