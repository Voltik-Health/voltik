-- =============================================================================
-- VOLTIK · dados FICTÍCIOS para desenvolvimento (nunca usar em produção)
-- Correr como voltik_migracoes depois de aplicar as migrações.
-- =============================================================================
BEGIN;

INSERT INTO organizacoes (id, nome_empresa, nif, setor_atividade, total_licencas_seats, data_inicio_contrato, data_fim_contrato)
VALUES ('10000000-0000-4000-8000-000000000001', 'Metalúrgica do Vale (fictícia)', '500000001', 'Metalomecânica', 40, '2026-01-01', '2026-12-31');

INSERT INTO utilizadores (id, email, palavra_passe_hash, nome_completo) VALUES
 ('20000000-0000-4000-8000-000000000001', 'manuel.sousa@exemplo.pt', NULL, 'Manuel Sousa'),
 ('20000000-0000-4000-8000-000000000002', 'ines.sousa@exemplo.pt', '$argon2id$exemplo', 'Inês Sousa'),
 ('20000000-0000-4000-8000-000000000003', 'ana.ribeiro@exemplo.pt', '$argon2id$exemplo', 'Ana Ribeiro'),
 ('20000000-0000-4000-8000-000000000004', 'luis.teixeira@exemplo.pt', '$argon2id$exemplo', 'Luís Teixeira');

INSERT INTO identidades_externas (utilizador_id, fornecedor, id_externo, email_fornecedor)
VALUES ('20000000-0000-4000-8000-000000000001', 'google', 'google-sub-0001', 'manuel.sousa@exemplo.pt');

INSERT INTO perfis_utilizador (utilizador_id, tipo_perfil, organizacao_id) VALUES
 ('20000000-0000-4000-8000-000000000001', 'paciente', NULL),
 ('20000000-0000-4000-8000-000000000002', 'cuidador', NULL),
 ('20000000-0000-4000-8000-000000000003', 'profissional_saude', NULL),
 ('20000000-0000-4000-8000-000000000004', 'gestor_empresa', '10000000-0000-4000-8000-000000000001');

INSERT INTO pacientes (id, utilizador_id, organizacao_id, data_nascimento, profissao, tipo_diabetes, terapia_atual)
VALUES ('30000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000001', '1968-03-02', 'Serralheiro', 'tipo_1', 'insulina_multiplas_doses');

INSERT INTO contactos_emergencia (paciente_id, nome_completo, grau_parentesco, telefone)
VALUES ('30000000-0000-4000-8000-000000000001', 'Inês Sousa', 'Filha', '+351910000000');

INSERT INTO vinculos_cuidador (paciente_id, cuidador_utilizador_id, nivel_permissao)
VALUES ('30000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000002', 'acesso_completo');

INSERT INTO profissionais_saude (id, utilizador_id, numero_ordem_cedula, especialidade)
VALUES ('40000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000003', 'CED-00001', 'Endocrinologia');

INSERT INTO atribuicoes_paciente_profissional (paciente_id, profissional_id)
VALUES ('30000000-0000-4000-8000-000000000001', '40000000-0000-4000-8000-000000000001');

INSERT INTO dispositivos (id, paciente_id, tipo_dispositivo, marca_modelo)
VALUES ('50000000-0000-4000-8000-000000000001', '30000000-0000-4000-8000-000000000001', 'sensor_cgm', 'Simulador BLE Voltik');

-- 3 horas de leituras, uma por minuto, com uma descida suave no fim
INSERT INTO leituras_glicemia (paciente_id, dispositivo_id, data_hora, valor_mg_dl, tendencia_seta, uuid_idempotencia)
SELECT '30000000-0000-4000-8000-000000000001', '50000000-0000-4000-8000-000000000001',
       now() - make_interval(mins => 180 - g),
       round(125 + 12 * sin(g / 20.0) - greatest(0, g - 150) * 0.4)::int,
       CASE WHEN g > 150 THEN 'descida_moderada' ELSE 'estavel' END,
       'seed-' || g
FROM generate_series(1, 180) AS g;

INSERT INTO alimentos (id, nome_alimento, fonte_origem) VALUES
 ('60000000-0000-4000-8000-000000000001', 'Arroz de pato', 'portugues_tradicional');
INSERT INTO porcoes_alimento (id, alimento_id, descricao_porcao, hidratos_carbono_g, calorias_kcal)
VALUES ('61000000-0000-4000-8000-000000000001', '60000000-0000-4000-8000-000000000001', '1 prato', 65, 620);

INSERT INTO refeicoes_diario (id, paciente_id, tipo_refeicao, data_refeicao, estado_consumo, total_hidratos_g, total_calorias_kcal)
VALUES ('70000000-0000-4000-8000-000000000001', '30000000-0000-4000-8000-000000000001', 'almoco', current_date, 'consumida', 65, 620);
INSERT INTO itens_refeicao (refeicao_id, alimento_id, porcao_id, quantidade_porcoes, hidratos_calculados_g, calorias_calculadas_kcal)
VALUES ('70000000-0000-4000-8000-000000000001', '60000000-0000-4000-8000-000000000001', '61000000-0000-4000-8000-000000000001', 1, 65, 620);

INSERT INTO consentimentos (utilizador_id, tipo_consentimento, versao_documento)
VALUES ('20000000-0000-4000-8000-000000000001', 'processamento_ia_externa', '1.0');

COMMIT;
