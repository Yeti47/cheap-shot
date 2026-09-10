# Analysis environment for the BK7252 firmware dump.
#
# Everything needed to disassemble/decompile the camera firmware, so nothing has
# to be installed on the host.  Ghidra needs JDK 21; the Fedora host ships JDK 25,
# which Ghidra refuses to run on.
FROM eclipse-temurin:21-jdk

ARG GHIDRA_VERSION=12.1.3
ARG GHIDRA_BUILD=20260817

ENV DEBIAN_FRONTEND=noninteractive \
    GHIDRA_HOME=/opt/ghidra \
    PATH=/opt/ghidra:/opt/ghidra/support:/opt/venv/bin:$PATH

RUN apt-get update && apt-get install -y --no-install-recommends \
        unzip curl ca-certificates \
        radare2 binwalk \
        gcc-arm-none-eabi binutils-arm-none-eabi \
        python3 python3-venv python3-pip \
        xxd less file \
    && rm -rf /var/lib/apt/lists/*

RUN curl -fsSL -o /tmp/ghidra.zip \
        "https://github.com/NationalSecurityAgency/ghidra/releases/download/Ghidra_${GHIDRA_VERSION}_build/ghidra_${GHIDRA_VERSION}_PUBLIC_${GHIDRA_BUILD}.zip" \
    && unzip -q /tmp/ghidra.zip -d /opt \
    && mv "/opt/ghidra_${GHIDRA_VERSION}_PUBLIC" /opt/ghidra \
    && rm /tmp/ghidra.zip

RUN python3 -m venv /opt/venv && /opt/venv/bin/pip install --no-cache-dir capstone

WORKDIR /work
CMD ["/bin/bash"]
