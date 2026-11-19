-- deploy/sql/v0.2.4/upgrade.sql — the split attrs migration
-- (polyforge#174): the one-time move of the flat `split.origin` /
-- `split.unit` attrs keys to the nested dot paths every reader walks
-- (the list filter's json_extract, the attrs write's `if`), so the
-- tickets the pre-fix `pf file create --attr` filed become listable by
-- the split check's own query again. Scope: the WHOLE database, every
-- project and every status, exactly these two keys — other attrs,
-- other dotted keys and statuses are not touched. Refusal: a row whose
-- nested `split` shape conflicts (a same-key different value, or a
-- `split` parent that is no object) refuses the WHOLE batch — one
-- transaction, nothing applied; resolve the row by hand and re-deploy.
-- Rollback reads the before-image table split_attrs_before_w174, which
-- holds exactly the rows this migration changes.

-- 1. the whole-batch refusal, first: one conflict aborts the
--    transaction (the CHECK fires on the inserted 'refuse' row).
CREATE TABLE w174_refuse(x TEXT CHECK (x <> 'refuse'));
INSERT INTO w174_refuse (x)
  SELECT CASE WHEN
         (json_type(w.attrs, '$.split') IS NOT NULL
          AND json_type(w.attrs, '$.split') <> 'object')
      OR (json_type(w.attrs, '$."split.origin"') IS NOT NULL
          AND json_type(w.attrs, '$.split.origin') IS NOT NULL
          AND json_extract(w.attrs, '$."split.origin"')
              IS NOT json_extract(w.attrs, '$.split.origin'))
      OR (json_type(w.attrs, '$."split.unit"') IS NOT NULL
          AND json_type(w.attrs, '$.split.unit') IS NOT NULL
          AND json_extract(w.attrs, '$."split.unit"')
              IS NOT json_extract(w.attrs, '$.split.unit'))
       THEN 'refuse' ELSE 'ok' END
    FROM work_items w
   WHERE json_type(w.attrs, '$."split.origin"') IS NOT NULL
      OR json_type(w.attrs, '$."split.unit"') IS NOT NULL;
DROP TABLE w174_refuse;

-- 2. the before-image: exactly the rows this migration changes, their
--    pre-migration attrs whole (the rollback's whole input).
CREATE TABLE IF NOT EXISTS split_attrs_before_w174 (
  id    TEXT PRIMARY KEY,
  attrs TEXT NOT NULL
);
INSERT OR REPLACE INTO split_attrs_before_w174 (id, attrs)
  SELECT id, attrs FROM work_items
   WHERE json_type(attrs, '$."split.origin"') IS NOT NULL
      OR json_type(attrs, '$."split.unit"') IS NOT NULL;

-- 3. the move: each flat key becomes the nested dot path it names
--    (json_set creates the missing `split` object, or writes into the
--    existing one — the guard already refused every conflicting
--    shape, so a same-key double shape keeps one nested copy), and the
--    flat key goes.
UPDATE work_items
   SET attrs = json_remove(json_set(attrs, '$.split.origin',
                        json_extract(attrs, '$."split.origin"')),
                           '$."split.origin"')
 WHERE json_type(attrs, '$."split.origin"') IS NOT NULL;
UPDATE work_items
   SET attrs = json_remove(json_set(attrs, '$.split.unit',
                        json_extract(attrs, '$."split.unit"')),
                           '$."split.unit"')
 WHERE json_type(attrs, '$."split.unit"') IS NOT NULL;
