#!/usr/bin/env ruby
# frozen_string_literal: true

require "fileutils"
require "json"
require "yaml"
require "discourse_theme"

DiscourseTheme::Cli.settings_file = File.expand_path("~/.discourse_theme")

def fetch_env!(key)
  ENV.fetch(key) { abort "Missing environment variable: #{key}" }
end

THEME_DIR = fetch_env!("THEME_DIR")
THEME_NAME = fetch_env!("THEME_NAME")
SITE_URL = fetch_env!("DISCOURSE_URL")
API_KEY = fetch_env!("DISCOURSE_API_KEY")

config = DiscourseTheme::Config.new(DiscourseTheme::Cli.settings_file)
settings = config[THEME_DIR]
settings.url = SITE_URL
settings.api_key = API_KEY

client = DiscourseTheme::Client.new(THEME_DIR, settings, reset: false)

begin
  theme_list = client.get_themes_list
rescue => e
  DiscourseTheme::UI.error("Failed to query themes: #{e.message}")
  raise
end

existing_theme = nil
if (stored_id = settings.theme_id) && stored_id > 0
  existing_theme = theme_list.find { |t| t["id"] == stored_id }
end

if !existing_theme
  existing_theme = theme_list.find { |t| t["name"] == THEME_NAME }
  if existing_theme
    settings.theme_id = existing_theme["id"]
  end
end

uploader = DiscourseTheme::Uploader.new(
  dir: THEME_DIR,
  client: client,
  theme_id: settings.theme_id.zero? ? nil : settings.theme_id,
  components: settings.components,
)

begin
  DiscourseTheme::UI.progress("Syncing #{THEME_NAME} from #{THEME_DIR}...")
  new_id = uploader.upload_full_theme(skip_migrations: false)
  if settings.theme_id.zero?
    settings.theme_id = new_id
  end
  DiscourseTheme::UI.success("Theme id #{settings.theme_id} is ready")
rescue => e
  DiscourseTheme::UI.error("Initial sync failed: #{e.message}")
  raise
end

uploader = DiscourseTheme::Uploader.new(
  dir: THEME_DIR,
  client: client,
  theme_id: settings.theme_id,
  components: settings.components,
)

watcher = DiscourseTheme::Watcher.new(dir: THEME_DIR, uploader: uploader)
DiscourseTheme::UI.progress("Watching #{THEME_DIR} for changes (theme id #{settings.theme_id})...")
watcher.watch
