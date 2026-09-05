FROM golang:1.27.1-bookworm
ARG USERNAME=vscode
ARG USER_UID=1000
ARG USER_GID=1000
RUN apt-get update && apt-get install -y --no-install-recommends \
      git ca-certificates curl sudo postgresql-client less vim-tiny openssh-client \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid $USER_GID $USERNAME \
    && useradd --uid $USER_UID --gid $USER_GID -m -s /bin/bash $USERNAME \
    && echo "$USERNAME ALL=(root) NOPASSWD:ALL" > /etc/sudoers.d/$USERNAME && chmod 0440 /etc/sudoers.d/$USERNAME \
    && mkdir -p /go/pkg/mod /home/$USERNAME/.cache/go-build \
    && chown -R $USERNAME:$USERNAME /go /home/$USERNAME
ENV GOPATH=/go GOMODCACHE=/go/pkg/mod GOFLAGS=-buildvcs=false
USER $USERNAME
WORKDIR /workspaces/konnyaku
