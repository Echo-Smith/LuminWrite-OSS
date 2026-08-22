-- 073: 素材库文件夹/分组管理
-- 1. material_folders: 用户素材文件夹（支持嵌套）
-- 2. user_materials.folder_id: 素材所属文件夹（NULL = 根目录）

-- ─── 1. 素材文件夹表 ─────────────────────────────────────
CREATE TABLE IF NOT EXISTS material_folders (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         VARCHAR(64) NOT NULL,
    name            VARCHAR(128) NOT NULL,
    parent_id       UUID REFERENCES material_folders(id) ON DELETE CASCADE,  -- NULL = 根目录
    sort_order      INTEGER NOT NULL DEFAULT 0,
    description     TEXT,
    material_count  INTEGER DEFAULT 0,  -- 缓存计数
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mf_user_id ON material_folders (user_id);
CREATE INDEX IF NOT EXISTS idx_mf_parent ON material_folders (user_id, parent_id);
CREATE UNIQUE INDEX IF NOT EXISTS uk_mf_user_parent_name ON material_folders (user_id, COALESCE(parent_id, '00000000-0000-0000-0000-000000000000'), name);

-- ─── 2. user_materials 添加 folder_id ────────────────────
ALTER TABLE user_materials
    ADD COLUMN IF NOT EXISTS folder_id UUID REFERENCES material_folders(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_um_folder ON user_materials (user_id, folder_id);
