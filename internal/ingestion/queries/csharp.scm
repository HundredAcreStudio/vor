; =============================================================================
; vor — C# symbol, import, and call queries
; tree-sitter-c-sharp >= 0.23
;
; Symbols: each type gets both a "with modifiers" pattern (to capture
; public/private/protected/internal) and a bare fallback. Tree-sitter
; queries can't make a child node optional in a single pattern, so
; duplication is the cost of correctness.
; =============================================================================

; ---- types with modifiers ----

(class_declaration
  (modifier) @symbol.modifiers
  name: (identifier) @symbol.name
) @symbol.def

(interface_declaration
  (modifier) @symbol.modifiers
  name: (identifier) @symbol.name
) @symbol.def

(struct_declaration
  (modifier) @symbol.modifiers
  name: (identifier) @symbol.name
) @symbol.def

(enum_declaration
  (modifier) @symbol.modifiers
  name: (identifier) @symbol.name
) @symbol.def

(record_declaration
  (modifier) @symbol.modifiers
  name: (identifier) @symbol.name
) @symbol.def

(method_declaration
  (modifier) @symbol.modifiers
  name: (identifier) @symbol.name
  parameters: (parameter_list) @symbol.params
) @symbol.def

(constructor_declaration
  (modifier) @symbol.modifiers
  name: (identifier) @symbol.name
  parameters: (parameter_list) @symbol.params
) @symbol.def

; ---- bare fallbacks ----

(class_declaration
  name: (identifier) @symbol.name
) @symbol.def

(interface_declaration
  name: (identifier) @symbol.name
) @symbol.def

(struct_declaration
  name: (identifier) @symbol.name
) @symbol.def

(enum_declaration
  name: (identifier) @symbol.name
) @symbol.def

(record_declaration
  name: (identifier) @symbol.name
) @symbol.def

(method_declaration
  name: (identifier) @symbol.name
  parameters: (parameter_list) @symbol.params
) @symbol.def

(constructor_declaration
  name: (identifier) @symbol.name
  parameters: (parameter_list) @symbol.params
) @symbol.def

; ---- namespaces (block + file-scoped C# 10+) ----

(namespace_declaration
  name: (qualified_name) @symbol.name
) @symbol.def

(namespace_declaration
  name: (identifier) @symbol.name
) @symbol.def

(file_scoped_namespace_declaration
  name: (qualified_name) @symbol.name
) @symbol.def

(file_scoped_namespace_declaration
  name: (identifier) @symbol.name
) @symbol.def

; ---- imports ----

(using_directive
  (qualified_name) @import.module
) @import.statement

(using_directive
  (identifier) @import.module
) @import.statement

; ---- calls ----

(invocation_expression
  function: (identifier) @call.target
  arguments: (argument_list) @call.arguments
) @call.site

(invocation_expression
  function: (member_access_expression
    expression: (identifier) @call.receiver
    name: (identifier) @call.target
  )
  arguments: (argument_list) @call.arguments
) @call.site

(invocation_expression
  function: (member_access_expression
    expression: (invocation_expression)
    name: (identifier) @call.target
  )
  arguments: (argument_list) @call.arguments
) @call.site

(object_creation_expression
  type: (identifier) @call.target
  arguments: (argument_list) @call.arguments
) @call.site
