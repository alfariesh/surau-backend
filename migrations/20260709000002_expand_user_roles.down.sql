-- Reverse of the role expansion: repatriate the new roles to 'user' so no row
-- violates the narrower constraint, then restore the prior CHECK.
UPDATE users
SET role = 'user'
WHERE role IN ('curator', 'scholar_reviewer');

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_role_check;

ALTER TABLE users
    ADD CONSTRAINT users_role_check
        CHECK (role IN ('user', 'editor', 'admin'));
