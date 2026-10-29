-- deploy/sql/v2.20.8/upgrade.sql — machine-heartbeat
-- permission model: a machine is identified by its operator, not by a
-- machine user.
--
-- Applied by .polyforge/deploy.sh INSIDE the switch window as ONE SQLite
-- transaction (see docs/upgrade.md). machines.user_id goes FIRST: while
-- that column exists it is a reference to the row's machine user and the
-- DELETE below could not remove the user the row no longer needs. The
-- DELETE then removes only heartbeat-minted users (kind='machine' with an
-- @machines.local email) that nothing else references; an
-- administrator-created machine user (a different email) and every
-- referenced mac_ user stay.
ALTER TABLE machines DROP COLUMN user_id;

DELETE FROM users
 WHERE kind = 'machine'
   AND email LIKE '%@machines.local'
   AND NOT EXISTS (SELECT 1 FROM api_keys k WHERE k.user_id = users.id)
   AND NOT EXISTS (SELECT 1 FROM project_members pm WHERE pm.user_id = users.id)
   AND NOT EXISTS (SELECT 1 FROM messages msg WHERE msg.sender_id = users.id)
   AND NOT EXISTS (SELECT 1 FROM conversation_members cm WHERE cm.user_id = users.id)
   AND NOT EXISTS (SELECT 1 FROM work_items wi WHERE wi.reporter = users.id OR wi.assignee = users.id)
   AND NOT EXISTS (SELECT 1 FROM wi_events e WHERE e.actor = users.id)
   AND NOT EXISTS (SELECT 1 FROM memories mem WHERE mem.owner_user_id = users.id)
   AND NOT EXISTS (SELECT 1 FROM machines m WHERE m.operator_id = users.id);
