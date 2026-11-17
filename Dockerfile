FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    bash \
    build-essential \
    ca-certificates \
    clang \
    coreutils \
    curl \
    git \
    libicu74 \
    libc6-dev \
    libssl3t64 \
    llvm \
    nasm \
    nodejs \
    npm \
    openjdk-25-jdk-headless \
    pkg-config \
    procps \
    python3 \
    python3-pip \
    rustc \
    cargo \
    tar \
    unzip \
    util-linux \
    zlib1g \
    xz-utils \
    && rm -rf /var/lib/apt/lists/*

ENV GO_VERSION=1.26.1
ENV GO_DOWNLOAD_URL=https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz

RUN curl -fsSL "$GO_DOWNLOAD_URL" -o go.tar.gz \
    && tar -C /usr/local -xzf go.tar.gz \
    && rm go.tar.gz

ENV PATH=$PATH:/usr/local/go/bin
ENV GOPATH=/go
ENV PATH=$PATH:$GOPATH/bin
ENV GOROOT=/usr/local/go

RUN mkdir -p "$GOPATH/src" "$GOPATH/bin" && chmod -R 777 "$GOPATH"

ENV DOTNET_ROOT=/usr/share/dotnet \
    DOTNET_CLI_TELEMETRY_OPTOUT=1 \
    DOTNET_NOLOGO=1 \
    DOTNET_SKIP_FIRST_TIME_EXPERIENCE=1 \
    DOTNET_GENERATE_ASPNET_CERTIFICATE=false \
    NUGET_XMLDOC_MODE=skip

RUN curl -fsSL https://dot.net/v1/dotnet-install.sh -o /tmp/dotnet-install.sh \
    && bash /tmp/dotnet-install.sh --channel 10.0 --install-dir "$DOTNET_ROOT" \
    && rm /tmp/dotnet-install.sh \
    && ln -s "$DOTNET_ROOT/dotnet" /usr/bin/dotnet \
    && dotnet --info

RUN groupadd --gid 2000 compiler \
    && useradd --uid 2000 --gid 2000 --create-home --shell /bin/bash compiler \
    && groupadd --gid 2001 runner \
    && useradd --uid 2001 --gid 2001 --create-home --shell /bin/bash runner \
    && mkdir -p /workspace /scratch \
    && chown -R compiler:compiler /workspace /scratch

WORKDIR /workspace

CMD ["sleep", "infinity"]
