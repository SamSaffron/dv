FROM discourse/discourse_dev:release

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update
RUN apt-get install -y --no-install-recommends vim ripgrep zip openssh-server

RUN npm i -g @openai/codex
RUN npm i -g @google/gemini-cli
RUN npm i -g @charmland/crush
RUN npm i -g opencode-ai@latest
RUN npm i -g @sourcegraph/amp

RUN sudo -H -u discourse /bin/bash -lc 'curl -fsSL https://claude.ai/install.sh | bash'
RUN sudo -H -u discourse /bin/bash -lc 'curl -LsSf https://aider.chat/install.sh | sh'
RUN sudo -H -u discourse /bin/bash -lc 'curl -fsS https://cursor.com/install | bash'

RUN chown discourse:discourse /var/www
RUN sudo -H -u discourse /bin/bash -lc 'git clone https://github.com/discourse/discourse.git /var/www/discourse'
RUN sudo -H -u discourse /bin/bash -lc 'cd /var/www/discourse && bundle && pnpm install'

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

# Configure SSH daemon
RUN mkdir -p /var/run/sshd
RUN mkdir -p /etc/service/sshd && \
    tee /etc/service/sshd/run > /dev/null <<'EOF'
#!/bin/bash
exec /usr/sbin/sshd -D
EOF
RUN chmod +x /etc/service/sshd/run

RUN mkdir -p /etc/runit/3.d && \
    tee /etc/runit/3.d/04-sshd > /dev/null <<'EOF'
#!/bin/bash
sv stop sshd
EOF
RUN chmod +x /etc/runit/3.d/04-sshd

# Configure SSH for discourse user
RUN echo "discourse:password" | chpasswd
RUN mkdir -p /home/discourse/.ssh
RUN chown discourse:discourse /home/discourse/.ssh
RUN chmod 700 /home/discourse/.ssh

# Configure SSH daemon settings
RUN tee /etc/ssh/sshd_config > /dev/null <<'EOF'
Port 22
Protocol 2
HostKey /etc/ssh/ssh_host_rsa_key
HostKey /etc/ssh/ssh_host_dsa_key
HostKey /etc/ssh/ssh_host_ecdsa_key
HostKey /etc/ssh/ssh_host_ed25519_key
UsePrivilegeSeparation yes
KeyRegenerationInterval 3600
ServerKeyBits 1024
SyslogFacility AUTH
LogLevel INFO
LoginGraceTime 120
PermitRootLogin no
StrictModes yes
RSAAuthentication yes
PubkeyAuthentication yes
IgnoreRhosts yes
RhostsRSAAuthentication no
HostbasedAuthentication no
PermitEmptyPasswords no
ChallengeResponseAuthentication no
PasswordAuthentication yes
X11Forwarding yes
X11DisplayOffset 10
PrintMotd no
PrintLastLog yes
TCPKeepAlive yes
AcceptEnv LANG LC_*
Subsystem sftp /usr/lib/openssh/sftp-server
UsePAM yes
EOF

# Generate SSH host keys
RUN ssh-keygen -A

ENV LEFTHOOK=1
ENV DISCOURSE_DEV_ALLOW_ANON_TO_IMPERSONATE=1
# allows qunit to work without CI being set to 1 which breaks a bunch of agents
ENV DISCOURSE_DISABLE_BROWSER_SANDBOX=1

ENTRYPOINT ["/sbin/boot"]
