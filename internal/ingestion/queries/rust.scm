; =============================================================================
; repowise — Rust symbol and import queries
; tree-sitter-rust >= 0.23
; =============================================================================

; ---------------------------------------------------------------------------
; Symbols
; ---------------------------------------------------------------------------

(function_item
  name: (identifier) @symbol.name
  parameters: (parameters) @symbol.params
) @symbol.def

(struct_item
  name: (type_identifier) @symbol.name
) @symbol.def

(enum_item
  name: (type_identifier) @symbol.name
) @symbol.def

(trait_item
  name: (type_identifier) @symbol.name
) @symbol.def

; impl block — the "type" field identifies what is being implemented
(impl_item
  type: (type_identifier) @symbol.name
) @symbol.def

(const_item
  name: (identifier) @symbol.name
) @symbol.def

(type_item
  name: (type_identifier) @symbol.name
) @symbol.def

(mod_item
  name: (identifier) @symbol.name
) @symbol.def

(macro_definition
  name: (identifier) @symbol.name
) @symbol.def

(function_item
  (visibility_modifier) @symbol.modifiers
  name: (identifier) @symbol.name
) @symbol.def

; Visibility for non-function items (struct, enum, trait, const, type,
; mod). Each gets its own pattern because tree-sitter Query syntax can't
; capture an optional named field inside a single pattern.
(struct_item
  (visibility_modifier) @symbol.modifiers
  name: (type_identifier) @symbol.name
) @symbol.def

(enum_item
  (visibility_modifier) @symbol.modifiers
  name: (type_identifier) @symbol.name
) @symbol.def

(trait_item
  (visibility_modifier) @symbol.modifiers
  name: (type_identifier) @symbol.name
) @symbol.def

(const_item
  (visibility_modifier) @symbol.modifiers
  name: (identifier) @symbol.name
) @symbol.def

(type_item
  (visibility_modifier) @symbol.modifiers
  name: (type_identifier) @symbol.name
) @symbol.def

(mod_item
  (visibility_modifier) @symbol.modifiers
  name: (identifier) @symbol.name
) @symbol.def

; ---------------------------------------------------------------------------
; Imports
; ---------------------------------------------------------------------------

(use_declaration
  argument: (_) @import.module
) @import.statement

; ---------------------------------------------------------------------------
; Calls
; ---------------------------------------------------------------------------

(call_expression
  function: (identifier) @call.target
  arguments: (arguments) @call.arguments
) @call.site

(call_expression
  function: (field_expression
    value: (identifier) @call.receiver
    field: (field_identifier) @call.target
  )
  arguments: (arguments) @call.arguments
) @call.site

(call_expression
  function: (scoped_identifier
    name: (identifier) @call.target
  )
  arguments: (arguments) @call.arguments
) @call.site

(call_expression
  function: (field_expression
    value: (call_expression)
    field: (field_identifier) @call.target
  )
  arguments: (arguments) @call.arguments
) @call.site

; Macro invocation: println!(...), vec![...]
(macro_invocation
  macro: (identifier) @call.target
) @call.site
