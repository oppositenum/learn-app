ALTER TABLE speech_outputs
ADD COLUMN audio_data_url text NOT NULL DEFAULT ''
CHECK (octet_length(audio_data_url) <= 25165824);
