# Tier 2 — Application Benchmarks
# Invokes make in each benchmark subdirectory that has a Makefile.

PLATFORM ?= auto
CUDA_ARCH ?=

ifeq ($(PLATFORM),auto)
  PLATFORM_ARG :=
else
  PLATFORM_ARG := PLATFORM=$(PLATFORM)
endif

ifneq ($(CUDA_ARCH),)
  CUDA_ARCH_ARG := CUDA_ARCH=$(CUDA_ARCH)
else
  CUDA_ARCH_ARG :=
endif

SUBDIRS := $(patsubst %/Makefile,%,$(wildcard */Makefile))

.PHONY: all clean $(SUBDIRS)

all: $(SUBDIRS)

$(SUBDIRS):
	$(MAKE) -C $@ $(PLATFORM_ARG) $(CUDA_ARCH_ARG)

clean:
	for b in $(SUBDIRS); do \
		$(MAKE) -C $$b clean $(PLATFORM_ARG) $(CUDA_ARCH_ARG); \
	done
