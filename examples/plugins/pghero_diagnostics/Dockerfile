# Use the official PgHero image as the base: it already ships ruby + the pghero,
# activerecord and pg gems, so there is no gem compile / build-essential needed —
# we only drop in our diagnostics script. (The image also contains a Rails web
# app we don't use; honey overrides the entrypoint with its /honey-plugin-init
# shim and runs the argv from plugin.cue, so the Rails server never starts.)
#
# Built by hand (honey has no build step):
#   docker build -t honey-plugin-pghero:latest examples/plugins/pghero_diagnostics/
FROM ankane/pghero:v3.8.0

COPY diagnose.rb /app/diagnose.rb

# No ENTRYPOINT/CMD override needed: honey replaces the entrypoint with its
# bind-mounted /honey-plugin-init shim and runs `ruby /app/diagnose.rb ...`.
