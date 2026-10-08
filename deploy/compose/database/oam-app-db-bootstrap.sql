-- Opt-in OAM runtime role. Run as the schema/database owner AFTER migrations.
-- Inputs: OAM_APP_DB_USER / OAM_APP_DB_PASSWORD or psql -v oam_user=... -v oam_password=...
-- Existing database owner, Gateway roles, schema ownership and shared PUBLIC grants are preserved.
\set ON_ERROR_STOP on
\if :{?oam_user}
\else
\getenv oam_user OAM_APP_DB_USER
\endif
\if :{?oam_password}
\else
\getenv oam_password OAM_APP_DB_PASSWORD
\endif
\if :{?oam_user}
\else
DO $abort$ BEGIN RAISE EXCEPTION 'OAM_APP_DB_USER is required'; END $abort$;
\endif
\if :{?oam_password}
\else
DO $abort$ BEGIN RAISE EXCEPTION 'OAM_APP_DB_PASSWORD is required'; END $abort$;
\endif
SELECT length(:'oam_user') = 0 OR octet_length(:'oam_user') > 63 OR length(:'oam_password') = 0 AS empty_input \gset
\if :empty_input
DO $abort$ BEGIN RAISE EXCEPTION 'OAM runtime role must be 1-63 bytes and its password must not be empty'; END $abort$;
\endif
BEGIN;
SELECT NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'oam_user') AS create_role \gset
\if :create_role
CREATE ROLE :"oam_user" LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD :'oam_password';
\endif
-- Refuse repurposing a privileged/member/owner role. Never silently demote an existing DBA.
SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'oam_user'
  AND (rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls))
  OR EXISTS (SELECT 1 FROM pg_auth_members WHERE member = (SELECT oid FROM pg_roles WHERE rolname = :'oam_user'))
  OR EXISTS (SELECT 1 FROM pg_database WHERE datdba = (SELECT oid FROM pg_roles WHERE rolname = :'oam_user'))
  OR EXISTS (SELECT 1 FROM pg_namespace WHERE nspowner = (SELECT oid FROM pg_roles WHERE rolname = :'oam_user'))
  OR EXISTS (SELECT 1 FROM pg_class WHERE relowner = (SELECT oid FROM pg_roles WHERE rolname = :'oam_user'))
  AS unsafe_role \gset
\if :unsafe_role
DO $abort$ BEGIN RAISE EXCEPTION 'Use a new unprivileged OAM runtime role, not a database owner, member or DBA'; END $abort$;
\endif
SELECT has_schema_privilege(:'oam_user', 'public', 'CREATE') AS inherited_create \gset
\if :inherited_create
DO $abort$ BEGIN RAISE EXCEPTION 'Runtime role inherits public CREATE; review shared grants before provisioning'; END $abort$;
\endif
ALTER ROLE :"oam_user" LOGIN PASSWORD :'oam_password';
ALTER ROLE :"oam_user" SET search_path = public;
SELECT format('GRANT CONNECT ON DATABASE %I TO %I', current_database(), :'oam_user') \gexec
GRANT USAGE ON SCHEMA public TO :"oam_user";
-- Explicit current OAM tables: no access to unrelated public or Gateway tables.
GRANT SELECT, INSERT, UPDATE, DELETE ON
 public.users, public.loxilb_instances, public.api_tokens, public.logs,
 public.alerts, public.acknowledgments, public.system_settings, public.system_config,
 public.login_attempts, public.instance_snapshots, public.instance_snapshot_schedules,
 public.appliance_operations, public.appliance_challenges, public.appliance_audit
 TO :"oam_user";
REVOKE ALL ON public.schema_migrations FROM :"oam_user";
GRANT SELECT ON public.schema_migrations TO :"oam_user";
-- Serial/identity sequences owned by only the named application tables.
SELECT DISTINCT format('GRANT USAGE, SELECT ON SEQUENCE %I.%I TO %I', n.nspname, s.relname, :'oam_user')
 FROM pg_class s JOIN pg_namespace n ON n.oid=s.relnamespace
 JOIN pg_depend d ON d.objid=s.oid AND d.classid='pg_class'::regclass
 JOIN pg_class t ON t.oid=d.refobjid
 JOIN pg_namespace tn ON tn.oid=t.relnamespace
 WHERE s.relkind='S' AND n.nspname='public' AND tn.nspname='public'
 AND d.refclassid='pg_class'::regclass AND d.deptype IN ('a','i') AND t.relname IN
 ('users','loxilb_instances','api_tokens','logs','alerts','acknowledgments','system_settings','system_config',
 'login_attempts','instance_snapshots','instance_snapshot_schedules','appliance_operations','appliance_challenges','appliance_audit') \gexec
SELECT has_table_privilege(:'oam_user', 'public.schema_migrations', 'INSERT,UPDATE,DELETE,TRUNCATE')
 OR EXISTS (SELECT 1 FROM pg_namespace WHERE nspname IN ('aigw','aigw_mgmt')
  AND has_schema_privilege(:'oam_user', oid, 'USAGE,CREATE')) AS inherited_unsafe_access \gset
\if :inherited_unsafe_access
DO $abort$ BEGIN RAISE EXCEPTION 'Runtime role inherits migration or Gateway access; review existing grants'; END $abort$;
\endif
COMMIT;
