; =============================================================================
; vor — C symbol and import queries
; Re-uses tree-sitter-cpp since C is a subset of that grammar.
; =============================================================================

(function_definition
  declarator: (function_declarator
    declarator: (identifier) @symbol.name
    parameters: (parameter_list) @symbol.params
  )
) @symbol.def

(struct_specifier
  name: (type_identifier) @symbol.name
) @symbol.def

(enum_specifier
  (type_identifier) @symbol.name
) @symbol.def

(preproc_include
  path: (system_lib_string) @import.module
) @import.statement

(preproc_include
  path: (string_literal) @import.module
) @import.statement

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
