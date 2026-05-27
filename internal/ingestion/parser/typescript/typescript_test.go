package typescript

import (
	"context"
	"slices"
	"testing"

	"github.com/repowise-dev/repowise-go/internal/ingestion/models"
)

func TestParse_TypeScript(t *testing.T) {
	src := []byte(`import { useState } from "react";
import type { User } from "./types";
import * as fs from "node:fs";

export function greet(name: string): string {
    return "hello " + name;
}

export const square = (n: number): number => n * n;

function unexportedHelper(): void {}

export class Calculator {
    private base: number;

    constructor(base: number) {
        this.base = base;
    }

    public add(a: number, b: number): number {
        return a + b + this.base;
    }

    private secret(): void {}
}

export interface Repository {
    save(): Promise<void>;
}

export type ID = string | number;

export enum Status {
    Active,
    Inactive,
}
`)
	fi := models.FileInfo{Path: "src/demo.ts", Language: "typescript"}
	parsed, err := (&Parser{}).Parse(context.Background(), fi, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	names := symbolNames(parsed.Symbols)
	for _, want := range []string{"greet", "square", "unexportedHelper", "Calculator", "add", "secret", "Repository", "ID", "Status"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing symbol %q in %v", want, names)
		}
	}

	for _, s := range parsed.Symbols {
		switch s.Name {
		case "greet":
			if s.Kind != models.KindFunction {
				t.Errorf("greet kind = %v", s.Kind)
			}
			if !s.IsExportedSymbol {
				t.Errorf("greet should be exported")
			}
		case "square":
			if s.Kind != models.KindFunction {
				t.Errorf("square kind = %v, want function (arrow)", s.Kind)
			}
		case "unexportedHelper":
			if s.IsExportedSymbol {
				t.Errorf("unexportedHelper IsExportedSymbol = true, want false")
			}
		case "Calculator":
			if s.Kind != models.KindClass {
				t.Errorf("Calculator kind = %v, want class", s.Kind)
			}
		case "add":
			if s.Kind != models.KindMethod {
				t.Errorf("add kind = %v, want method", s.Kind)
			}
			if s.ParentName == nil || *s.ParentName != "Calculator" {
				t.Errorf("add parent = %v, want Calculator", s.ParentName)
			}
			if s.Visibility != models.VisibilityPublic {
				t.Errorf("add visibility = %v, want public", s.Visibility)
			}
			if s.ID != "src/demo.ts::Calculator::add" {
				t.Errorf("add ID = %q", s.ID)
			}
		case "secret":
			if s.Visibility != models.VisibilityPrivate {
				t.Errorf("secret visibility = %v, want private", s.Visibility)
			}
		case "Repository":
			if s.Kind != models.KindInterface {
				t.Errorf("Repository kind = %v, want interface", s.Kind)
			}
		case "ID":
			if s.Kind != models.KindTypeAlias {
				t.Errorf("ID kind = %v, want type_alias", s.Kind)
			}
		case "Status":
			if s.Kind != models.KindEnum {
				t.Errorf("Status kind = %v, want enum", s.Kind)
			}
		}
	}

	// Imports.
	got := importModules(parsed.Imports)
	for _, w := range []string{"react", "./types", "node:fs"} {
		if !slices.Contains(got, w) {
			t.Errorf("missing import %q in %v", w, got)
		}
	}
	// IsRelative: ./types yes, others no.
	for _, im := range parsed.Imports {
		if im.ModulePath == "./types" && !im.IsRelative {
			t.Errorf("./types IsRelative = false, want true")
		}
		if im.ModulePath == "react" && im.IsRelative {
			t.Errorf("react IsRelative = true, want false")
		}
	}

	// Exports: greet, square, Calculator, Repository, ID, Status (six top-
	// level exports). unexportedHelper and class methods should NOT appear.
	for _, w := range []string{"greet", "square", "Calculator", "Repository", "ID", "Status"} {
		if !slices.Contains(parsed.Exports, w) {
			t.Errorf("missing export %q in %v", w, parsed.Exports)
		}
	}
	if slices.Contains(parsed.Exports, "unexportedHelper") {
		t.Errorf("unexportedHelper leaked into exports: %v", parsed.Exports)
	}
	if slices.Contains(parsed.Exports, "add") {
		t.Errorf("add (method) leaked into exports: %v", parsed.Exports)
	}
}

func TestParse_TSX(t *testing.T) {
	src := []byte(`import React from "react";

export function Hello(props: { name: string }) {
    return <h1>Hello {props.name}</h1>;
}
`)
	fi := models.FileInfo{Path: "src/Hello.tsx", Language: "typescript"}
	parsed, err := (&Parser{}).Parse(context.Background(), fi, src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	names := symbolNames(parsed.Symbols)
	if !slices.Contains(names, "Hello") {
		t.Errorf("missing TSX symbol Hello: %v", names)
	}
}

func symbolNames(s []models.Symbol) []string {
	out := make([]string, 0, len(s))
	for _, sy := range s {
		out = append(out, sy.Name)
	}
	return out
}

func importModules(imps []models.Import) []string {
	out := make([]string, 0, len(imps))
	for _, im := range imps {
		out = append(out, im.ModulePath)
	}
	return out
}
