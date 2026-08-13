ARG GO_IMAGE
FROM ${GO_IMAGE}

ARG NODE_ARCH
ARG NODE_VERSION
ARG WAILS_VERSION

ENV PATH="/opt/node/bin:/go/bin:/usr/local/go/bin:${PATH}"

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        git \
        libdigest-sha-perl \
        libgtk-3-dev \
        libwebkit2gtk-4.1-dev \
        pkg-config \
        xz-utils \
        zip \
    && node_archive="node-v${NODE_VERSION}-linux-${NODE_ARCH}.tar.xz" \
    && curl -fsSLo "/tmp/${node_archive}" \
        "https://nodejs.org/dist/v${NODE_VERSION}/${node_archive}" \
    && mkdir -p /opt/node \
    && tar -xJf "/tmp/${node_archive}" -C /opt/node --strip-components=1 \
    && rm -f "/tmp/${node_archive}" \
    && go install "github.com/wailsapp/wails/v2/cmd/wails@${WAILS_VERSION}" \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
