#!/usr/bin/env sh
# One image, two commands: the first argument names the binary, and anything else
# is passed to the server.
#
# A leading ./ is stripped before matching, because `command: ["./worker"]` is the
# obvious thing to write and matching only the bare form sent it to the default
# branch — which started the server with "./worker" as a stray argument it
# ignored. The wrong binary, running quietly, with nothing in the logs naming the
# mistake.
name="${1#./}"
case "$name" in
  executor|worker)
    shift
    exec "./$name" "$@"
    ;;
  *)
    exec ./executor "$@"
    ;;
esac
