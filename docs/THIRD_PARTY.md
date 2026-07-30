# Third-party notices and design references

## MapSCII

NeProto Constellation includes an original, dependency-free, offline Braille
world-map renderer inspired by the interaction and terminal-cartography ideas
of [rastapasta/mapscii](https://github.com/rastapasta/mapscii).

MapSCII is distributed under the MIT License. NeProto does not vendor or run
the MapSCII JavaScript implementation, its tile client, its remote HTTP tile
source, or its Telnet service. The NeProto renderer uses original approximate
coastline control points and operates without network access.

Copyright notice for the referenced project:

```text
Copyright (c) 2016 Robert Matzinger
SPDX-License-Identifier: MIT
```

## eDEX-UI

The multi-panel cinematic layout is an independent implementation visually
inspired by [GitSquared/edex-ui](https://github.com/GitSquared/edex-ui). No
eDEX-UI source code, themes, fonts, images, or application assets are bundled.

## tcell

The terminal screen and input engine uses
[github.com/gdamore/tcell/v2](https://pkg.go.dev/github.com/gdamore/tcell/v2)
under its upstream license as recorded by the Go module dependency metadata.

## NeProto Web dashboard foundation

The NeProto Web interface is derived from Mohammed Arham Khan's
`next-shadcn-admin-dashboard` template. The retained MIT license and copyright
notice are stored in `neproto-web/LICENSE`. NeProto-specific branding,
navigation, localization, server views, and release integration are maintained
in this repository.
