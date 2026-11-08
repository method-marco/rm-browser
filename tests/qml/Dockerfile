# Headless test image for the QML frontend: make qmltest
FROM debian:bookworm
RUN apt-get update -qq && apt-get install -y -qq --no-install-recommends \
      qml-qt6 qt6-declarative-dev-tools qml6-module-qttest qml6-module-qtquick \
      qml6-module-qtquick-controls qml6-module-qtquick-layouts qml6-module-qtquick-templates \
      qml6-module-qtquick-window qml6-module-qtqml-workerscript qt6-qpa-plugins \
      fonts-noto-core fonts-ebgaramond >/dev/null && rm -rf /var/lib/apt/lists/*
ENV QT_QPA_PLATFORM=offscreen
