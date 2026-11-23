# Vendored: brand fonts

The brand fonts (handoff §6.5) are self-hosted: the UI makes no request to another origin. These
files are copied unmodified from the Fontsource npm packages. Do not edit them; to upgrade, replace
them from a newer release and update this file. Only the Latin subset goldie's copy needs is
vendored. Both fonts are licensed under the SIL Open Font License 1.1, which allows bundling them
with software under any licence as long as the licence travels with the fonts (`LICENSE-*.txt`).

- `@fontsource-variable/inter` 5.3.0 (OFL-1.1), published 2026-07-19
  - Source: https://registry.npmjs.org/@fontsource-variable/inter/-/inter-5.3.0.tgz
  - Tarball sha256: `d26710dd38e7217484a1d47ee76c024977ec550548876f26d06ed1844af09a14`
  - Tarball integrity (npm): `sha512-OupL48va4JNofb97w6NYeF9S7W/kHNKM0Er8Dem5nqi4jeOLrVJDoE8tZEpnMJmtkvNbB1EIPPwHcdkF6b1oUA==`
- `@fontsource/space-grotesk` 5.3.0 (OFL-1.1), published 2026-07-19
  - Source: https://registry.npmjs.org/@fontsource/space-grotesk/-/space-grotesk-5.3.0.tgz
  - Tarball sha256: `2adb650694b6387b1862fee8b086a3a47ce9f32c10f20b8ec109aa1fcdfd07b7`
  - Tarball integrity (npm): `sha512-ksnGizDPXIDuvqcTYTSrmZ+evx9sDlS8rp7+42BQ7wU+spt3twEoXfbJz672C+5CLg6VeUQwRy5RXshWb67LcQ==`

| File | From | sha256 |
|---|---|---|
| `inter-latin-wght-normal.woff2` | `files/inter-latin-wght-normal.woff2` (Inter, variable weight) | `3100e775e8616cd2611beecfa23a4263d7037586789b43f035236a2e6fbd4c62` |
| `space-grotesk-latin-600-normal.woff2` | `files/space-grotesk-latin-600-normal.woff2` | `685bbbf69fa616df1ef81847c85fc76be097ddfb3468ff2257be54511ab3130f` |
| `LICENSE-Inter.txt` | `LICENSE` of the Inter package | `3b0a5fca3d17942cde889069889dedbbbd075e9b599968c82a95f4d944e9b345` |
| `LICENSE-SpaceGrotesk.txt` | `LICENSE` of the Space Grotesk package | `18a4de52385f6b988782639d5d0cc1326e5a8c2de9a7f01d7b20d9aedcc60943` |

JetBrains Mono (ids and commands, §6.5) is not vendored yet: the App shell shows no identifiers.
