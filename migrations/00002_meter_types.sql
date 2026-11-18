-- Meter type and subtype dictionary (spec п. 9). Admins can add more via the API.

-- +goose Up
INSERT INTO meter_types (code, name) VALUES
    ('water',       'Вода'),
    ('gas',         'Газ'),
    ('electricity', 'Электроэнергия'),
    ('heat',        'Тепло');

INSERT INTO meter_subtypes (type_id, code, name, unit)
SELECT t.id, s.code, s.name, s.unit
FROM (VALUES
    ('water',       'hot_water',        'Горячая вода',           'm3'),
    ('water',       'cold_water',       'Холодная вода',          'm3'),
    ('water',       'sewage',           'Канализация',            'm3'),
    ('gas',         'natural_gas',      'Природный газ',          'm3'),
    ('gas',         'lpg',              'Сжиженный газ',          'm3'),
    ('electricity', 'active_energy',    'Активная энергия',       'kWh'),
    ('electricity', 'reactive_energy',  'Реактивная энергия',     'kvarh'),
    ('heat',        'heat',             'Тепловая энергия',       'Gcal')
) AS s (type_code, code, name, unit)
JOIN meter_types t ON t.code = s.type_code;

-- +goose Down
DELETE FROM meter_subtypes
WHERE code IN ('hot_water', 'cold_water', 'sewage', 'natural_gas', 'lpg',
               'active_energy', 'reactive_energy', 'heat');
DELETE FROM meter_types WHERE code IN ('water', 'gas', 'electricity', 'heat');
