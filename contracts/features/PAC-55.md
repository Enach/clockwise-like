# Contract — PAC-55: a per-user 12/24-hour time format

- **Spec**: `docs/specs/PAC-55.md` (challenged; OQ-1 to OQ-5 closed)
- **Manifest**: `contracts/features/PAC-55.yaml`
- **Builds on**: PAC-24 (per-user settings, PATCH merge, the shared
  `&settingsProperties` map), merged as Enach/clockwise-like#204.

## 1. The change

One property on the settings document:

```yaml
timeFormat:
  type: string
  enum: ['12h', '24h']
```

It is added to the shared property map, so `Settings` (GET, PATCH response)
and `SettingsUpdate` (PATCH body) both carry it, and it is in `Settings`'
required list: a GET always returns it. Nothing else in the contract changes.

## 2. Why this shape

- **One setting, two values, no "unset" (spec §3, owner's "go for
  simplicity").** An enum of exactly the two choices. `""` is not in it: the
  spec says changing the preference means choosing the other value, so an
  empty value is a 400 like any other value outside the set (spec AC-5), the
  same rule PAC-24 applied to the other settings with no empty state.
- **The default is a column default, not a client convention.** The column is
  `NOT NULL DEFAULT '12h'`, so every existing user and every new row reads
  `12h` — exactly today's display — and the client never has to interpret an
  absent value. The spec's "until the choice is known, show 12-hour" is a
  client loading rule, not a wire state.
- **Names.** `timeFormat` follows the camelCase of every other settings
  property. The values `12h`/`24h` are short, unambiguous and stable; a
  boolean (`use24Hour`) was rejected because a third format would need a new
  property, and because a boolean has no natural "default" reading in a
  document where `false` is also a written value.
- **Display only.** The server formats nothing with it. The Slack recap and the
  booking confirmation email already use 24-hour times composed by the server
  (spec §5) and are out of scope; the public booking page never reads it.
- **Merge semantics come from PAC-24.** Absent means unchanged (AC-4), present
  means set exactly (AC-2), `null` is a 400 (AC-5), and only a changed value is
  validated — a stored value is always one of the two because of the CHECK
  constraint.

## 3. What a challenger should attack

- Whether any consumer renders the document strictly and breaks on an added
  required property (the web's generated zod `Settings` is strict: the web must
  regenerate before or with the server — spec §7's order).
- Whether `""` being a 400 contradicts spec AC-5 or AC-7 of PAC-24 (it should
  match both).
- Whether the e2e criteria (AC-8, AC-10, AC-11) can be written against the
  surfaces as they render today.
