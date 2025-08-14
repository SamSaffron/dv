FROM discourse/discourse_dev:release

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update
RUN apt-get install -y --no-install-recommends vim ripgrep

RUN npm install -g @openai/codex
RUN npm install -g @google/gemini-cli

RUN sudo -H -u discourse /bin/bash -lc 'curl -fsSL https://claude.ai/install.sh | bash'
RUN sudo -H -u discourse /bin/bash -lc 'curl -LsSf https://aider.chat/install.sh | sh'
RUN sudo -H -u discourse /bin/bash -lc 'curl -fsS https://cursor.com/install | bash'

RUN chown discourse:discourse /var/www
RUN sudo -H -u discourse /bin/bash -lc 'git clone https://github.com/discourse/discourse.git /var/www/discourse'
RUN sudo -H -u discourse /bin/bash -lc 'cd /var/www/discourse && bundle && pnpm install'

RUN /sbin/boot & \
    sleep 10 && \
    sudo -H -u discourse /bin/bash -lc 'cd /var/www/discourse && bin/rake db:create' && \
    sudo -H -u discourse /bin/bash -lc 'cd /var/www/discourse && bin/rake db:migrate' && \
    sudo -H -u discourse /bin/bash -lc 'cd /var/www/discourse && RAILS_ENV=test bin/rake db:migrate' && \
    pkill -f "/sbin/boot" || true

RUN sudo -H -u discourse /bin/bash -lc "cd /var/www/discourse && npx playwright install-deps && npx playwright install"

RUN mkdir -p /etc/service/ember-cli && \
    tee /etc/service/ember-cli/run > /dev/null <<'EOF'
#!/bin/bash

cd /var/www/discourse
HOME=/home/discourse USER=discourse exec chpst -u discourse:discourse -U discourse:discourse bin/ember-cli

sudo -u discourse:discourse 
EOF
RUN chmod +x /etc/service/ember-cli/run

RUN mkdir -p /etc/runit/3.d && \
    tee /etc/runit/3.d/02-ember-cli > /dev/null <<'EOF'
#!/bin/bash
sv stop ember-cli
EOF
RUN chmod +x /etc/runit/3.d/02-ember-cli

ENTRYPOINT ["/sbin/boot"]

