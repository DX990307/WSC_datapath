# Makefile for tier2/parboil_sgemm — Parboil SGEMM benchmark
#
# Supported platforms:
#   PLATFORM=cuda   — NVIDIA CUDA  (default on Linux without ROCm)
#   PLATFORM=rocm   — AMD ROCm/HIP (default when /opt/rocm exists)
#   PLATFORM=metal  — Apple Metal  (default on Darwin)
#
# Usage:
#   make                   # auto-detect platform
#   make PLATFORM=cuda
#   make PLATFORM=rocm
#   make PLATFORM=metal
#   make clean

# ---------------------------------------------------------------------------
# Auto-detect platform
# ---------------------------------------------------------------------------
ifndef PLATFORM
  ifeq ($(shell uname -s),Darwin)
    PLATFORM := metal
  else ifneq ($(wildcard /opt/rocm),)
    PLATFORM := rocm
  else
    PLATFORM := cuda
  endif
endif

# ---------------------------------------------------------------------------
# Platform-specific settings
# ---------------------------------------------------------------------------
TARGET := parboil_sgemm

ifeq ($(PLATFORM),cuda)
  CC     := nvcc
  SRC    := parboil_sgemm.cu
  CUDA_ARCH ?= sm_90
  CFLAGS := -O3 -std=c++14 -arch=$(CUDA_ARCH)

else ifeq ($(PLATFORM),rocm)
  CC     := hipcc
  SRC    := parboil_sgemm.hip
  CFLAGS := -O3 -std=c++14

else ifeq ($(PLATFORM),metal)
  CC     := clang++
  SRC    := parboil_sgemm_metal.mm
  CFLAGS := -O2 -std=c++14 \
             -framework Metal \
             -framework Foundation \
             -framework MetalKit \
             -fobjc-arc

else
  $(error Unknown PLATFORM=$(PLATFORM). Use cuda, rocm, or metal.)
endif

# ---------------------------------------------------------------------------
# Targets
# ---------------------------------------------------------------------------
.PHONY: all clean

all: $(TARGET)

$(TARGET): $(SRC)
	$(CC) $(CFLAGS) $< -o $@

clean:
	rm -f $(TARGET)
