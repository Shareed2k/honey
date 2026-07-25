#!/usr/bin/env ruby
# frozen_string_literal: true

# PgHero read-only diagnostics for honey's docker plugin runtime.
# Reads DATABASE_URL from the environment (honey injects it via plugin.cue's
# `env:` field), runs an allow-listed set of PgHero checks, and prints ONE JSON
# object to stdout. Never prints the connection string. Every check is wrapped
# so one failing check (e.g. a missing extension) does not abort the rest.

require "json"
require "optparse"
require "active_record"
require "pghero"

options = { checks: [] }
OptionParser.new do |o|
  o.on("--checks VALUE") { |v| options[:checks] = v.split(",").map(&:strip).reject(&:empty?) }
end.parse!

database_url = ENV["DATABASE_URL"].to_s
if database_url.empty?
  puts JSON.generate(
    success: false,
    error: { code: "DATABASE_URL_MISSING", message: "DATABASE_URL is empty or unset" },
  )
  exit 1
end

# Standalone (no Rails) setup: PgHero lazily builds its :primary database from
# PGHERO_DATABASE_URL, establishing its own ActiveRecord connection — no
# ActiveRecord::Base.establish_connection or PgHero.config needed (there is no
# PgHero.config= setter in 3.x). `active_record` must be required before
# `pghero` (done above) or PgHero::ActiveRecord is uninitialized.
ENV["PGHERO_DATABASE_URL"] ||= database_url

# Map allow-listed check names → PgHero method. Only these may run (no arbitrary
# method from config). Method presence is checked at call time.
CHECKS = {
  "connections"       => :connections,
  "total_connections" => :total_connections,
  "connection_states" => :connection_states,
  "connection_sources" => :connection_sources,
  "running_queries"   => :running_queries,
  "long_running_queries" => :long_running_queries,
  "slow_queries"      => :slow_queries,        # needs pg_stat_statements
  "index_usage"       => :index_usage,
  "unused_indexes"    => :unused_indexes,
  "missing_indexes"   => :missing_indexes,
  "invalid_indexes"   => :invalid_indexes,
  "maintenance"       => :maintenance_info,
  "space"             => :relation_sizes,
  "database_size"     => :database_size,
  "table_sizes"       => :table_sizes,
}.freeze

def normalize_json(value)
  case value
  when Array then value.map { |v| normalize_json(v) }
  when Hash  then value.each_with_object({}) { |(k, v), h| h[k.to_s] = normalize_json(v) }
  else
    if value.respond_to?(:as_json)
      aj = value.as_json
      aj.equal?(value) ? value : normalize_json(aj)
    elsif value.respond_to?(:to_h)
      normalize_json(value.to_h)
    else
      value
    end
  end
end

def safe_call(method)
  unless PgHero.respond_to?(method)
    return { ok: false, error: { code: "METHOD_ABSENT", message: "PgHero has no ##{method}" } }
  end
  { ok: true, data: normalize_json(PgHero.public_send(method)) }
rescue => e
  { ok: false, error: { class: e.class.name, message: e.message } }
end

requested = options[:checks]
requested = %w[connections index_usage space running_queries] if requested.empty?

results = {}
requested.each do |name|
  method = CHECKS[name]
  results[name] =
    if method.nil?
      { ok: false, error: { code: "UNSUPPORTED_CHECK", message: "unsupported check: #{name}" } }
    else
      safe_call(method)
    end
end

failed = results.count { |_, r| !r[:ok] }
puts JSON.generate(
  success: failed.zero?,
  summary: "PgHero diagnostics: #{results.size} checks run, #{failed} failed",
  risk: failed.zero? ? "low" : "medium",
  outputs: results,
)
