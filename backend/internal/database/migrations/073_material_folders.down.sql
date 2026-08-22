-- 073 down: 回滚素材库文件夹

-- 先清除 folder_id 引用
UPDATE user_materials SET folder_id = NULL WHERE folder_id IS NOT NULL;

ALTER TABLE user_materials
    DROP COLUMN IF EXISTS folder_id;

DROP TABLE IF EXISTS material_folders;
