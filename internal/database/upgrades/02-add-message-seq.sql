-- v2: Add message internal id
CREATE TABLE message_copy (
    chat_uid TEXT,
    chat_receiver TEXT,
    msg_seq TEXT,
    msg_id TEXT,
    mxid TEXT UNIQUE,
    sender TEXT,
    timestamp BIGINT,
    sent BOOLEAN,
    error error_type,
    type TEXT,
    PRIMARY KEY (chat_uid, chat_receiver, msg_seq, msg_id),
    FOREIGN KEY (chat_uid, chat_receiver) REFERENCES portal(uid, receiver) ON DELETE CASCADE
);

INSERT INTO
    message_copy (
        chat_uid,
        chat_receiver,
        msg_seq,
        msg_id,
        mxid,
        sender,
        timestamp,
        sent,
        error,
        type
    )
SELECT
    chat_uid,
    chat_receiver,
    '0',
    msg_id,
    mxid,
    sender,
    timestamp,
    sent,
    error,
    type
FROM
    message;

DROP TABLE message;

ALTER TABLE
    message_copy RENAME TO message;
