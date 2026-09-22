-- 011: Remove seeded evaluation data

-- Delete all samples belonging to the seeded sets
DELETE FROM evaluation_samples
WHERE set_id IN (
    SELECT id FROM evaluation_sets
    WHERE name IN (
        '印月三谈·深度时评评测集',
        '申论·公考写作评测集',
        '小红书·种草内容评测集'
    )
);

-- Delete the seeded sets
DELETE FROM evaluation_sets
WHERE name IN (
    '印月三谈·深度时评评测集',
    '申论·公考写作评测集',
    '小红书·种草内容评测集'
);
