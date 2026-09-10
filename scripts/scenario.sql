CREATE DATABASE IF NOT EXISTS demo;
USE demo;

CREATE TABLE accounts (
  id      INT PRIMARY KEY,
  owner   VARCHAR(32) NOT NULL,
  balance INT NOT NULL
);
INSERT INTO accounts VALUES (1, 'alice', 1000), (2, 'bob', 500);

-- step
UPDATE demo.accounts SET balance = 900 WHERE id = 1;
