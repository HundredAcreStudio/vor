package java

import (
	"testing"

	"github.com/repowise-dev/repowise-go/internal/ingestion/graph/resolver"
	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

func newCtx(files []string, roots []string) resolver.Context {
	ctx := resolver.Context{
		Files:           map[string]bool{},
		JavaSourceRoots: roots,
	}
	for _, f := range files {
		ctx.Files[f] = true
	}
	return ctx
}

func TestJava_MavenLayout(t *testing.T) {
	ctx := newCtx([]string{
		"src/main/java/com/example/Main.java",
		"src/main/java/com/example/util/Greeter.java",
	}, nil)
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "src/main/java/com/example/Main.java", Language: "java"},
		models.Import{ModulePath: "com.example.util.Greeter"},
		ctx,
	)
	if len(got) != 1 || got[0] != "src/main/java/com/example/util/Greeter.java" {
		t.Errorf("maven import: %v", got)
	}
}

func TestJava_FlatLayout(t *testing.T) {
	// src/com/example/... layout, default roots include "src".
	ctx := newCtx([]string{
		"src/com/example/Main.java",
		"src/com/example/util/Greeter.java",
	}, nil)
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "src/com/example/Main.java", Language: "java"},
		models.Import{ModulePath: "com.example.util.Greeter"},
		ctx,
	)
	if len(got) != 1 || got[0] != "src/com/example/util/Greeter.java" {
		t.Errorf("flat import: %v", got)
	}
}

func TestJava_StdlibReturnsEmpty(t *testing.T) {
	ctx := newCtx([]string{"Main.java"}, nil)
	for _, mod := range []string{"java.util.List", "java.lang.String", "javax.servlet.Servlet"} {
		got := Resolver{}.Resolve(
			models.FileInfo{Path: "Main.java", Language: "java"},
			models.Import{ModulePath: mod},
			ctx,
		)
		if len(got) != 0 {
			t.Errorf("stdlib %q resolved: %v", mod, got)
		}
	}
}

func TestJava_ThirdPartyNotInAnalyzedSet(t *testing.T) {
	ctx := newCtx([]string{"Main.java"}, nil)
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "Main.java", Language: "java"},
		models.Import{ModulePath: "org.springframework.beans.BeanUtils"},
		ctx,
	)
	if len(got) != 0 {
		t.Errorf("third-party should not resolve: %v", got)
	}
}

func TestJava_CustomSourceRoot(t *testing.T) {
	ctx := newCtx([]string{
		"app/src/com/example/Main.java",
		"app/src/com/example/Util.java",
	}, []string{"app/src"})
	got := Resolver{}.Resolve(
		models.FileInfo{Path: "app/src/com/example/Main.java", Language: "java"},
		models.Import{ModulePath: "com.example.Util"},
		ctx,
	)
	if len(got) != 1 || got[0] != "app/src/com/example/Util.java" {
		t.Errorf("custom root: %v", got)
	}
}
