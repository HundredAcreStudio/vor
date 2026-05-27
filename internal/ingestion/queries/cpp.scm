; =============================================================================
; repowise — C++ symbol and import queries
; tree-sitter-cpp >= 0.23 (also used for .c files — C is a subset of this
; grammar for our purposes)
; =============================================================================

; ---------------------------------------------------------------------------
; Symbols
; ---------------------------------------------------------------------------

(function_definition
  declarator: (function_declarator
    declarator: (identifier) @symbol.name
    parameters: (parameter_list) @symbol.params
  )
) @symbol.def

(function_definition
  declarator: (function_declarator
    declarator: (field_identifier) @symbol.name
    parameters: (parameter_list) @symbol.params
  )
) @symbol.def

(function_definition
  declarator: (function_declarator
    declarator: (qualified_identifier
      name: (identifier) @symbol.name
    )
    parameters: (parameter_list) @symbol.params
  )
) @symbol.def

(function_definition
  declarator: (function_declarator
    declarator: (qualified_identifier
      name: (qualified_identifier
        name: (identifier) @symbol.name
      )
    )
    parameters: (parameter_list) @symbol.params
  )
) @symbol.def

(class_specifier
  name: (type_identifier) @symbol.name
) @symbol.def

(struct_specifier
  name: (type_identifier) @symbol.name
) @symbol.def

(enum_specifier
  (type_identifier) @symbol.name
) @symbol.def

(namespace_definition
  name: (namespace_identifier) @symbol.name
) @symbol.def

; ---------------------------------------------------------------------------
; Imports (#include directives)
; ---------------------------------------------------------------------------

(preproc_include
  path: (system_lib_string) @import.module
) @import.statement

(preproc_include
  path: (string_literal) @import.module
) @import.statement

; ---------------------------------------------------------------------------
; Calls
; ---------------------------------------------------------------------------

(call_expression
  function: (identifier) @call.target
  arguments: (argument_list) @call.arguments
) @call.site

(call_expression
  function: (field_expression
    argument: (identifier) @call.receiver
    field: (field_identifier) @call.target
  )
  arguments: (argument_list) @call.arguments
) @call.site

(call_expression
  function: (qualified_identifier
    name: (identifier) @call.target
  )
  arguments: (argument_list) @call.arguments
) @call.site

(call_expression
  function: (field_expression
    argument: (call_expression)
    field: (field_identifier) @call.target
  )
  arguments: (argument_list) @call.arguments
) @call.site
