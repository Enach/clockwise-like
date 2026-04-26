CREATE TABLE scheduling_links (
  id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_user_id     UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  slug              TEXT        NOT NULL UNIQUE,
  title             TEXT        NOT NULL,
  duration_options  INTEGER[]   NOT NULL DEFAULT '{30}',
  days_of_week      INTEGER[]   NOT NULL DEFAULT '{1,2,3,4,5}',
  window_start_time TIME        NOT NULL DEFAULT '09:00',
  window_end_time   TIME        NOT NULL DEFAULT '17:00',
  buffer_before     INTEGER     NOT NULL DEFAULT 0,
  buffer_after      INTEGER     NOT NULL DEFAULT 0,
  active            BOOLEAN     NOT NULL DEFAULT true,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE scheduling_link_hosts (
  id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  link_id      UUID        NOT NULL REFERENCES scheduling_links(id) ON DELETE CASCADE,
  user_id      UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  status       TEXT        NOT NULL DEFAULT 'accepted' CHECK (status IN ('pending','accepted','declined')),
  invited_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  responded_at TIMESTAMPTZ,
  UNIQUE (link_id, user_id)
);

CREATE TABLE bookings (
  id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  link_id      UUID        NOT NULL REFERENCES scheduling_links(id) ON DELETE CASCADE,
  booker_name  TEXT        NOT NULL,
  booker_email TEXT        NOT NULL,
  start_time   TIMESTAMPTZ NOT NULL,
  end_time     TIMESTAMPTZ NOT NULL,
  status       TEXT        NOT NULL DEFAULT 'confirmed' CHECK (status IN ('pending','confirmed','cancelled')),
  notes        TEXT        NOT NULL DEFAULT '',
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE booking_events (
  id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  booking_id        UUID        NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
  user_id           UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  calendar_event_id TEXT        NOT NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_scheduling_links_owner ON scheduling_links(owner_user_id);
CREATE INDEX idx_scheduling_links_slug  ON scheduling_links(slug);
CREATE INDEX idx_slh_link               ON scheduling_link_hosts(link_id);
CREATE INDEX idx_slh_user               ON scheduling_link_hosts(user_id);
CREATE INDEX idx_bookings_link          ON bookings(link_id);
CREATE INDEX idx_booking_events_booking ON booking_events(booking_id);
