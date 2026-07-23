package csr

import (
	"reflect"
	"testing"
)

func TestDefaultMatrixGeneratorIsReproducible(t *testing.T) {
	want := MakeMatrixGenerator(32, 96).GenerateMatrix()
	got := MakeMatrixGenerator(32, 96).GenerateMatrix()

	if !reflect.DeepEqual(got, want) {
		t.Fatal("default generators produced different CSR matrices")
	}
}

func TestMatrixGeneratorRestartsFromSeed(t *testing.T) {
	generator := MakeMatrixGeneratorWithSeed(32, 96, 7)
	want := generator.GenerateMatrix()
	got := generator.GenerateMatrix()

	if !reflect.DeepEqual(got, want) {
		t.Fatal("reusing one generator changed the generated CSR matrix")
	}
}

func TestMatrixGeneratorSeedControlsWorkload(t *testing.T) {
	a := MakeMatrixGeneratorWithSeed(32, 96, 7).GenerateMatrix()
	b := MakeMatrixGeneratorWithSeed(32, 96, 8).GenerateMatrix()

	if reflect.DeepEqual(a, b) {
		t.Fatal("different seeds unexpectedly produced the same CSR matrix")
	}
}
