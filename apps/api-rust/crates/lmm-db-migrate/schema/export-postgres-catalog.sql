SELECT jsonb_agg(
  jsonb_build_object(
    'name', table_class.relname,
    'columns', (
      SELECT jsonb_agg(
        jsonb_build_object(
          'name', attribute.attname,
          'type', format_type(attribute.atttypid, attribute.atttypmod),
          'not_null', attribute.attnotnull,
          'default', pg_get_expr(attribute_default.adbin, attribute_default.adrelid)
        ) ORDER BY attribute.attnum
      )
      FROM pg_attribute AS attribute
      LEFT JOIN pg_attrdef AS attribute_default
        ON attribute_default.adrelid = attribute.attrelid
       AND attribute_default.adnum = attribute.attnum
      WHERE attribute.attrelid = table_class.oid
        AND attribute.attnum > 0
        AND NOT attribute.attisdropped
    ),
    'indexes', (
      SELECT jsonb_agg(
        jsonb_build_object(
          'name', index_class.relname,
          'unique', index_metadata.indisunique,
          'primary', index_metadata.indisprimary,
          'columns', (
            SELECT jsonb_agg(quote_ident(key_attribute.attname) ORDER BY key.ordinality)
            FROM unnest(index_metadata.indkey) WITH ORDINALITY AS key(attnum, ordinality)
            JOIN pg_attribute AS key_attribute
              ON key_attribute.attrelid = table_class.oid
             AND key_attribute.attnum = key.attnum
            WHERE key.ordinality <= index_metadata.indnkeyatts
          ),
          'predicate', pg_get_expr(index_metadata.indpred, index_metadata.indrelid)
        ) ORDER BY index_class.relname
      )
      FROM pg_index AS index_metadata
      JOIN pg_class AS index_class ON index_class.oid = index_metadata.indexrelid
      WHERE index_metadata.indrelid = table_class.oid
    ),
    'foreign_keys', COALESCE((
      SELECT jsonb_agg(
        jsonb_build_object(
          'name', constraint_metadata.conname,
          'columns', (
            SELECT jsonb_agg(source_attribute.attname ORDER BY key.ordinality)
            FROM unnest(constraint_metadata.conkey) WITH ORDINALITY AS key(attnum, ordinality)
            JOIN pg_attribute AS source_attribute
              ON source_attribute.attrelid = constraint_metadata.conrelid
             AND source_attribute.attnum = key.attnum
          ),
          'referenced_table', referenced_table.relname,
          'referenced_columns', (
            SELECT jsonb_agg(referenced_attribute.attname ORDER BY key.ordinality)
            FROM unnest(constraint_metadata.confkey) WITH ORDINALITY AS key(attnum, ordinality)
            JOIN pg_attribute AS referenced_attribute
              ON referenced_attribute.attrelid = constraint_metadata.confrelid
             AND referenced_attribute.attnum = key.attnum
          ),
          'on_delete', CASE constraint_metadata.confdeltype
            WHEN 'a' THEN 'no_action'
            WHEN 'r' THEN 'restrict'
            WHEN 'c' THEN 'cascade'
            WHEN 'n' THEN 'set_null'
            WHEN 'd' THEN 'set_default'
          END,
          'deferrable', constraint_metadata.condeferrable,
          'initially_deferred', constraint_metadata.condeferred
        ) ORDER BY constraint_metadata.conname
      )
      FROM pg_constraint AS constraint_metadata
      JOIN pg_class AS referenced_table
        ON referenced_table.oid = constraint_metadata.confrelid
      WHERE constraint_metadata.conrelid = table_class.oid
        AND constraint_metadata.contype = 'f'
    ), '[]'::jsonb),
    'sequence', (
      SELECT jsonb_build_object(
        'name', sequence_class.relname,
        'owned_column', owned_attribute.attname,
        'default', pg_get_expr(owned_default.adbin, owned_default.adrelid)
      )
      FROM pg_depend AS ownership
      JOIN pg_class AS sequence_class
        ON sequence_class.oid = ownership.objid
       AND sequence_class.relkind = 'S'
      JOIN pg_namespace AS sequence_namespace
        ON sequence_namespace.oid = sequence_class.relnamespace
       AND sequence_namespace.nspname = 'public'
      JOIN pg_attribute AS owned_attribute
        ON owned_attribute.attrelid = ownership.refobjid
       AND owned_attribute.attnum = ownership.refobjsubid
      LEFT JOIN pg_attrdef AS owned_default
        ON owned_default.adrelid = owned_attribute.attrelid
       AND owned_default.adnum = owned_attribute.attnum
      WHERE ownership.refobjid = table_class.oid
        AND ownership.deptype = 'a'
    )
  ) ORDER BY table_class.relname
)
FROM pg_class AS table_class
JOIN pg_namespace AS table_namespace ON table_namespace.oid = table_class.relnamespace
WHERE table_namespace.nspname = 'public'
  AND table_class.relkind = 'r';
