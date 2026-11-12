
# Tweak the cli flags like so:

# Top Level
safe   #alias 's'
record #aliases 'rec', 'r'
keys   #aliases 'key', 'k'
query  #alias 'q'

# Sub-commands

## safe
- create (create a new empty safe)  # alias 'c'
- export (export entire safe)       
- list (list all records)           # alias 'l', 'ls'
- passwd (change passphrase)
- rekey


## record
- add       # alias 'a'
- del       # alias 'd'
- show      # alias 's'
- copy      # alias 'c'
- edit      # alias 'e'
- types 

## keys
- anything that should go here


## query
query <search param> [CLI FLAGS]


# New Features

I want a context mechanism that points to a current safe. If a context is set, all commands default to that safe. If no context is set, use the default safe location. So the context
under ~/.digisafe somehow.

## context (cmd)
- show (shows the current context)
- set (sets a context)
- clear (clears the current context)



