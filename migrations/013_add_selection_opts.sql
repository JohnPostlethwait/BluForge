-- A rip that fails and is later salvaged has to be repeated with the same
-- choices: the same titles, the same audio and subtitle languages, the same
-- names. The titles and names were already on the job; the language selections
-- were not, so a salvage could only offer to start the whole thing over.
ALTER TABLE rip_jobs ADD COLUMN selection_opts TEXT NOT NULL DEFAULT '';

-- The playlist a title came from. The rip checks it against makemkvcon's own
-- numbering, which is not stable between invocations; without it a repeated rip
-- would trust an index recorded hours earlier.
ALTER TABLE rip_jobs ADD COLUMN source_file TEXT NOT NULL DEFAULT '';
