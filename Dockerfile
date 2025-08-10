FROM discourse/discourse_dev:release

SHELL ["/bin/bash", "-o", "pipefail", "-c"]

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update
RUN apt-get install -y --no-install-recommends vim ripgrep
RUN sudo -H -u discourse /bin/bash -lc 'curl -fsS https://cursor.com/install | bash'
RUN chown discourse:discourse /var/www
RUN sudo -H -u discourse /bin/bash -lc 'git clone https://github.com/discourse/discourse.git /var/www/discourse'
RUN sudo -H -u discourse /bin/bash -lc 'cd /var/www/discourse && bundle && pnpm install'

RUN /sbin/boot & \
    sleep 10 && \
    sudo -H -u discourse /bin/bash -lc 'cd /var/www/discourse && bin/rake db:create' && \
    sudo -H -u discourse /bin/bash -lc 'cd /var/www/discourse && bin/rake db:migrate' && \
    pkill -f "/sbin/boot" || true

ENTRYPOINT ["/sbin/boot"]

