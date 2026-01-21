#!/bin/bash

APP_NAME="marix"
INSTALL_DIR="$HOME/.local/bin"
DESKTOP_DIR="$HOME/.local/share/applications"
ICON_DIR="$HOME/.local/share/icons/hicolor/scalable/apps"

echo "Installing $APP_NAME..."

mkdir -p "$INSTALL_DIR"
mkdir -p "$DESKTOP_DIR"
mkdir -p "$ICON_DIR"

cp marix "$INSTALL_DIR/"
chmod +x "$INSTALL_DIR/marix"

if [ -f "icon.svg" ]; then
    cp icon.svg "$ICON_DIR/marix.svg"
fi

cat > "$DESKTOP_DIR/marix.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=Marix
Comment=Secure SSH & SFTP Client
Exec=$INSTALL_DIR/marix
Icon=marix
Terminal=true
Categories=Utility;Network;
EOF

if command -v update-desktop-database >/dev/null; then
    update-desktop-database "$DESKTOP_DIR"
fi

echo "Installation complete! You can now launch Marix from your application menu."
