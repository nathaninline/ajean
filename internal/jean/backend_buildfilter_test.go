package jean

import "testing"

func TestCompiledFile(t *testing.T) {
	cases := map[string]string{
		`  Compiling CUDA source file ..\..\..\..\ggml\src\ggml-cuda\acc.cu...`: "acc.cu",
		`    ggml-threading.cpp`: "ggml-threading.cpp",
		`    ggml-quants.c`:      "ggml-quants.c",
		// Make (Linux) et Ninja.
		`[ 45%] Building CXX object src/CMakeFiles/llama.dir/llama.cpp.o`:                     "llama.cpp",
		`[123/456] Building CUDA object ggml/src/ggml-cuda/CMakeFiles/ggml-cuda.dir/acc.cu.o`: "acc.cu",
		// La ligne de commande nvcc géante ne doit PAS être prise pour un fichier.
		`  C:\...\nvcc.exe -x cu ... -o ggml-cuda.dir\Release\acc.obj "C:\...\acc.cu"`: "",
		`Building Custom Rule C:/ProgramData/jean/...`:                                 "",
		`-- UI: running npm install`:                                                   "",
	}
	for in, want := range cases {
		if got := compiledFile(in); got != want {
			t.Errorf("compiledFile(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildError(t *testing.T) {
	keep := []string{
		`acc.cu(12): error C2065: 'foo': undeclared identifier`,
		`LINK : fatal error LNK1104: cannot open file`,
		// gcc/clang (Linux).
		`/x/ggml.cpp:42:9: error: 'foo' was not declared in this scope`,
	}
	drop := []string{
		// Les warnings ne doivent PAS remonter (bruit tiers).
		`ggml.cpp(10): warning C4244: conversion`,
		`LINK : warning LNK4098: conflit entre la bibliothèque ...`,
		// Contient -D_CRT_SECURE_NO_WARNINGS mais n'est pas un diagnostic.
		`nvcc.exe ... -D_CRT_SECURE_NO_WARNINGS -DGGML_SHARED ... -o acc.obj`,
		`Compiling CUDA source file acc.cu...`,
		`C:\Program Files\NVIDIA GPU Computing Toolkit\CUDA\v13.3\bin\nvcc.exe`,
	}
	for _, l := range keep {
		if !reBuildError.MatchString(l) {
			t.Errorf("erreur attendue mais ratée: %q", l)
		}
	}
	for _, l := range drop {
		if reBuildError.MatchString(l) {
			t.Errorf("fausse erreur: %q", l)
		}
	}
}
