-- v3: Add message content
ALTER TABLE
    message
ADD
    COLUMN content TEXT;
