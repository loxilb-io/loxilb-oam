-- RBAC 3-role model.
-- Extends users.role with 'operator' and 'viewer'; 'user' is kept in the enum
-- as a legacy alias (code treats it as operator) so old rows/inserts don't
-- break, and existing 'user' rows are migrated to 'operator'.
-- New-user default becomes least-privilege 'viewer'.

USE loxioam;

ALTER TABLE users
    MODIFY role ENUM('admin', 'operator', 'viewer', 'user') DEFAULT 'viewer';

UPDATE users SET role = 'operator' WHERE role = 'user';
