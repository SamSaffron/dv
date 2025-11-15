FROM discourse/discourse_dev:release

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update
RUN apt-get install -y --no-install-recommends vim ripgrep zip inotify-tools chromium

RUN chown discourse:discourse /var/www
RUN sudo -H -u discourse /bin/bash -lc 'git clone https://github.com/discourse/discourse.git /var/www/discourse'
RUN sudo -H -u discourse /bin/bash -lc 'cd /var/www/discourse && bundle'
RUN sudo -H -u discourse /bin/bash -lc 'cd /var/www/discourse && pnpm install'

RUN npm i -g @openai/codex
RUN npm i -g @google/gemini-cli
RUN npm i -g @charmland/crush
RUN npm i -g opencode-ai@latest
RUN npm i -g @sourcegraph/amp
RUN npm i -g @github/copilot
RUN npm i -g @musistudio/claude-code-router
RUN npm i -g @kilocode/cli
RUN sudo -H -u discourse /bin/bash -lc 'curl -fsSL https://claude.ai/install.sh | bash'
RUN sudo -H -u discourse /bin/bash -lc 'curl -LsSf https://aider.chat/install.sh | sh'
RUN sudo -H -u discourse /bin/bash -lc 'curl -fsS https://cursor.com/install | bash'

RUN tee /tmp/seed_users.rb > /dev/null <<'EOF'
u = User.new(username: "admin", email: "admin@example.com")

u.password = "password"
u.name = "Admin"
u.active = true
u.save!(validate: false)

u.grant_admin!
u.change_trust_level!(4) 
u.email_tokens.update_all(confirmed: true)
u.activate

puts "Admin ready: username=#{u.username} email=#{u.email}"

(0..3).each do |i|
  username = "user#{i}"
  email = "#{username}@example.com"

  u = User.new(username: username, email: email)

  u.password = "password"
  u.name = username.capitalize if u.name.blank?
  u.active = true
  u.save!(validate: false)

  tl = i + 1
  u.change_trust_level!(tl) if u.trust_level < tl
  u.email_tokens.update_all(confirmed: true)
  u.activate

  puts "User ready: username=#{u.username} tl=#{u.trust_level}"
end
EOF

RUN /sbin/boot & \
    sleep 10 && \
    sudo -H -u discourse /bin/bash -lc 'cd /var/www/discourse && bin/rake db:create' && \
    sudo -H -u discourse /bin/bash -lc 'cd /var/www/discourse && bin/rake db:migrate' && \
    sudo -H -u discourse /bin/bash -lc 'cd /var/www/discourse && RAILS_ENV=test bin/rake db:migrate' && \
    sudo -H -u discourse /bin/bash -lc 'cd /var/www/discourse && bin/rails r /tmp/seed_users.rb' && \
    pkill -f "/sbin/boot" || true

RUN sudo -H -u discourse /bin/bash -lc "cd /var/www/discourse && npx playwright install-deps && npx playwright install"

RUN mkdir -p /etc/service/unicorn && \
    tee /etc/service/unicorn/run > /dev/null <<'EOF'
#!/bin/bash

cd /var/www/discourse
HOME=/home/discourse USER=discourse exec chpst -u discourse:discourse -U discourse:discourse bin/unicorn
EOF
RUN chmod +x /etc/service/unicorn/run

RUN mkdir -p /etc/runit/3.d && \
    tee /etc/runit/3.d/03-unicorn > /dev/null <<'EOF'
#!/bin/bash
sv stop unicorn
EOF
RUN chmod +x /etc/runit/3.d/03-unicorn

RUN mkdir -p /etc/service/ember-cli && \
    tee /etc/service/ember-cli/run > /dev/null <<'EOF'
#!/bin/bash

cd /var/www/discourse
HOME=/home/discourse UNICORN_PORT=9292 USER=discourse exec chpst -u discourse:discourse -U discourse:discourse bin/ember-cli
EOF
RUN chmod +x /etc/service/ember-cli/run

RUN mkdir -p /etc/runit/3.d && \
    tee /etc/runit/3.d/02-ember-cli > /dev/null <<'EOF'
#!/bin/bash
sv stop ember-cli
EOF
RUN chmod +x /etc/runit/3.d/02-ember-cli

ENV LEFTHOOK=1
ENV DISCOURSE_DEV_ALLOW_ANON_TO_IMPERSONATE=1
# allows qunit to work without CI being set to 1 which breaks a bunch of agents
ENV DISCOURSE_DISABLE_BROWSER_SANDBOX=1
ENV TESTEM_DEFAULT_BROWSER=Chromium 
ENV CHROME_BIN=/usr/bin/chromium
ENV UNICORN_PORT=9292

ENTRYPOINT ["/sbin/boot"]
