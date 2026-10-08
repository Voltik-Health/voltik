-- ===========================================================================
-- VOLTIK · MIGRAÇÃO 000001 · ESQUEMA INICIAL (29 tabelas)
-- PostgreSQL 17 · aplicada com golang-migrate pelo papel voltik_migracoes
-- Alterações face ao voltik_schema.sql v2:
--   · TIMESTAMP -> TIMESTAMPTZ (instantes com fuso horário)
--   · uuid_generate_v4() -> gen_random_uuid() (nativo, sem extensão)
--   · índices em todas as chaves estrangeiras usadas em pesquisas
--   · registo de auditoria só de inserção para o papel voltik_api
-- Autores: Afonso Carvalho & Rui Passos
-- ===========================================================================


-- ---------------------------------------------------------------------------
-- 1. MÓDULO: IDENTIDADE, ACESSO & MULTITENANCY CORPORATIVO (B2B / B2C)
-- ---------------------------------------------------------------------------
CREATE TABLE utilizadores (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email VARCHAR(255) UNIQUE NOT NULL,
    palavra_passe_hash VARCHAR(255), -- NULL quando a conta usa apenas login externo
    nome_completo VARCHAR(150) NOT NULL,
    telemovel VARCHAR(30),
    foto_perfil_url VARCHAR(500),
    estado_conta VARCHAR(20) NOT NULL DEFAULT 'ativo' CHECK (estado_conta IN ('ativo', 'suspenso', 'pendente', 'desativado')),
    ultimo_login_em TIMESTAMPTZ,
    criado_em TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    atualizado_em TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE identidades_externas (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    utilizador_id UUID NOT NULL REFERENCES utilizadores(id) ON DELETE CASCADE,
    fornecedor VARCHAR(20) NOT NULL CHECK (fornecedor IN ('microsoft', 'google', 'apple', 'facebook')),
    id_externo VARCHAR(255) NOT NULL, -- identificador "sub" devolvido pelo fornecedor OpenID Connect
    email_fornecedor VARCHAR(255),
    ligado_em TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ultimo_login_em TIMESTAMPTZ,
    CONSTRAINT uq_fornecedor_id_externo UNIQUE (fornecedor, id_externo),
    CONSTRAINT uq_utilizador_fornecedor UNIQUE (utilizador_id, fornecedor)
);
CREATE TABLE perfis_utilizador (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    utilizador_id UUID NOT NULL REFERENCES utilizadores(id) ON DELETE CASCADE,
    tipo_perfil VARCHAR(30) NOT NULL CHECK (tipo_perfil IN ('paciente', 'cuidador', 'profissional_saude', 'gestor_empresa', 'administrador')),
    organizacao_id UUID, -- preenchido apenas no perfil gestor_empresa
    perfil_ativo BOOLEAN NOT NULL DEFAULT TRUE,
    criado_em TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_utilizador_perfil UNIQUE (utilizador_id, tipo_perfil)
);

CREATE TABLE organizacoes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    nome_empresa VARCHAR(150) NOT NULL,
    nif VARCHAR(20) UNIQUE NOT NULL,
    setor_atividade VARCHAR(100),
    total_licencas_seats INT NOT NULL DEFAULT 10 CHECK (total_licencas_seats > 0),
    data_inicio_contrato DATE NOT NULL,
    data_fim_contrato DATE NOT NULL,
    estado VARCHAR(20) NOT NULL DEFAULT 'ativo' CHECK (estado IN ('ativo', 'suspenso', 'expirado')),
    criado_em TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE codigos_convite_empresa (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    organizacao_id UUID NOT NULL REFERENCES organizacoes(id) ON DELETE CASCADE,
    codigo VARCHAR(30) UNIQUE NOT NULL,
    criado_por_utilizador_id UUID REFERENCES utilizadores(id),
    utilizado_por_utilizador_id UUID REFERENCES utilizadores(id),
    estado VARCHAR(20) NOT NULL DEFAULT 'disponivel' CHECK (estado IN ('disponivel', 'utilizado', 'expirado', 'revogado')),
    data_expiracao TIMESTAMPTZ NOT NULL,
    utilizado_em TIMESTAMPTZ,
    criado_em TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE subscricoes_b2c (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    utilizador_id UUID NOT NULL REFERENCES utilizadores(id) ON DELETE CASCADE,
    paciente_beneficiario_id UUID, -- paciente coberto; o pagador pode ser o próprio ou um cuidador
    plano VARCHAR(30) NOT NULL CHECK (plano IN ('gratuito_base', 'premium_individual', 'premium_familiar')),
    preco_mensal DECIMAL(10,2) NOT NULL DEFAULT 0.00,
    data_inicio TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    data_fim TIMESTAMPTZ,
    renovacao_automatica BOOLEAN NOT NULL DEFAULT TRUE,
    estado VARCHAR(20) NOT NULL DEFAULT 'ativo' CHECK (estado IN ('ativo', 'cancelado', 'pendente_pagamento'))
);

-- ---------------------------------------------------------------------------
-- 2. MÓDULO: NÚCLEO CLÍNICO DO PACIENTE & REDE DE CUIDADORES
-- ---------------------------------------------------------------------------
CREATE TABLE pacientes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    utilizador_id UUID UNIQUE NOT NULL REFERENCES utilizadores(id) ON DELETE CASCADE,
    organizacao_id UUID REFERENCES organizacoes(id) ON DELETE SET NULL,
    data_nascimento DATE NOT NULL,
    genero VARCHAR(20) CHECK (genero IN ('masculino', 'feminino', 'outro')),
    profissao VARCHAR(100),
    tipo_diabetes VARCHAR(30) NOT NULL CHECK (tipo_diabetes IN ('tipo_1', 'tipo_2', 'gestacional', 'lada', 'modi')),
    terapia_atual VARCHAR(40) CHECK (terapia_atual IN ('insulina_multiplas_doses', 'bomba_insulina', 'antidiabeticos_orais', 'dieta')),
    glicemia_alvo_min_mg_dl INT NOT NULL DEFAULT 70 CHECK (glicemia_alvo_min_mg_dl >= 40),
    glicemia_alvo_max_mg_dl INT NOT NULL DEFAULT 180 CHECK (glicemia_alvo_max_mg_dl > glicemia_alvo_min_mg_dl),
    limiar_hipo_critica_mg_dl INT NOT NULL DEFAULT 55 CHECK (limiar_hipo_critica_mg_dl < glicemia_alvo_min_mg_dl),
    limiar_hiper_critica_mg_dl INT NOT NULL DEFAULT 250 CHECK (limiar_hiper_critica_mg_dl > glicemia_alvo_max_mg_dl),
    peso_kg DECIMAL(5,2),
    altura_cm INT,
    notas_clinicas TEXT,
    criado_em TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE contactos_emergencia (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    paciente_id UUID NOT NULL REFERENCES pacientes(id) ON DELETE CASCADE,
    nome_completo VARCHAR(150) NOT NULL,
    grau_parentesco VARCHAR(50),
    telefone VARCHAR(30) NOT NULL,
    prioridade_chamada INT NOT NULL DEFAULT 1,
    notificar_por_sms BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE vinculos_cuidador (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    paciente_id UUID NOT NULL REFERENCES pacientes(id) ON DELETE CASCADE,
    cuidador_utilizador_id UUID NOT NULL REFERENCES utilizadores(id) ON DELETE CASCADE,
    nivel_permissao VARCHAR(30) NOT NULL DEFAULT 'alertas_e_valores' CHECK (nivel_permissao IN ('apenas_alertas', 'alertas_e_valores', 'acesso_completo')),
    estado VARCHAR(20) NOT NULL DEFAULT 'ativo' CHECK (estado IN ('ativo', 'pendente_aprovacao', 'revogado')),
    data_vinculo TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    data_revogacao TIMESTAMPTZ,
    motivo_revogacao VARCHAR(255),
    CONSTRAINT uq_paciente_cuidador UNIQUE (paciente_id, cuidador_utilizador_id)
);

-- ---------------------------------------------------------------------------
-- 3. MÓDULO: TELEMETRIA EM TEMPO REAL & DISPOSITIVOS MÉDICOS (CGM & WEARABLES)
-- ---------------------------------------------------------------------------
CREATE TABLE dispositivos (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    paciente_id UUID NOT NULL REFERENCES pacientes(id) ON DELETE CASCADE,
    tipo_dispositivo VARCHAR(30) NOT NULL CHECK (tipo_dispositivo IN ('sensor_cgm', 'smartwatch', 'smartband', 'glicosimetro_manual')),
    marca_modelo VARCHAR(100) NOT NULL,
    numero_serie_mac VARCHAR(100),
    estado_conexao VARCHAR(25) NOT NULL DEFAULT 'ativo' CHECK (estado_conexao IN ('ativo', 'desconectado', 'a_aquecer', 'expirado', 'substituido')),
    nivel_bateria_percent INT CHECK (nivel_bateria_percent BETWEEN 0 AND 100),
    data_primeira_leitura TIMESTAMPTZ,
    data_expiracao_sensor TIMESTAMPTZ,
    ultima_sincronizacao TIMESTAMPTZ
);

CREATE TABLE leituras_glicemia (
    id BIGSERIAL PRIMARY KEY,
    paciente_id UUID NOT NULL REFERENCES pacientes(id) ON DELETE CASCADE,
    dispositivo_id UUID REFERENCES dispositivos(id) ON DELETE SET NULL,
    data_hora TIMESTAMPTZ NOT NULL,
    valor_mg_dl INT NOT NULL CHECK (valor_mg_dl BETWEEN 20 AND 550),
    tendencia_seta VARCHAR(25) NOT NULL DEFAULT 'estavel' CHECK (tendencia_seta IN ('subida_rapida', 'subida_moderada', 'estavel', 'descida_moderada', 'descida_rapida', 'sem_tendencia')),
    origem VARCHAR(30) NOT NULL DEFAULT 'sensor_cgm' CHECK (origem IN ('sensor_cgm', 'puncionamento_capilar_manual', 'healthkit_sync', 'health_connect_sync')),
    qualidade_sinal INT DEFAULT 100,
    uuid_idempotencia VARCHAR(100) UNIQUE,
    sincronizado_em TIMESTAMPTZ NOT NULL DEFAULT NOW() -- instante em que a leitura chegou ao servidor
);
CREATE INDEX idx_leituras_paciente_data ON leituras_glicemia (paciente_id, data_hora DESC);

CREATE TABLE registos_insulina (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    paciente_id UUID NOT NULL REFERENCES pacientes(id) ON DELETE CASCADE,
    refeicao_associada_id UUID,
    tipo_insulina VARCHAR(30) NOT NULL CHECK (tipo_insulina IN ('rapida_bolus', 'basal_lenta', 'mista_premisturada')),
    nome_marca_insulina VARCHAR(80),
    unidades_dose DECIMAL(4,1) NOT NULL CHECK (unidades_dose > 0),
    data_hora_administracao TIMESTAMPTZ NOT NULL,
    motivo_administracao VARCHAR(35) NOT NULL DEFAULT 'refeicao' CHECK (motivo_administracao IN ('refeicao', 'correcao_hiper', 'basal_programada', 'misto')),
    notas VARCHAR(255),
    criado_em TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE registos_atividade_fisica (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    paciente_id UUID NOT NULL REFERENCES pacientes(id) ON DELETE CASCADE,
    dispositivo_id UUID REFERENCES dispositivos(id) ON DELETE SET NULL,
    nome_atividade VARCHAR(100) NOT NULL,
    data_hora_inicio TIMESTAMPTZ NOT NULL,
    duracao_minutos INT NOT NULL CHECK (duracao_minutos > 0),
    nivel_intensidade VARCHAR(20) NOT NULL CHECK (nivel_intensidade IN ('leve', 'moderado', 'intenso')),
    passos_totais INT,
    frequencia_cardiaca_media_bpm INT,
    calorias_gastas_kcal INT,
    criado_em TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- 4. MÓDULO: NUTRIÇÃO & ALIMENTAÇÃO (ALINHADO COM A API FATSECRET)
-- ---------------------------------------------------------------------------
CREATE TABLE alimentos (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    fatsecret_food_id BIGINT UNIQUE,
    codigo_barras_ean VARCHAR(50) UNIQUE,
    nome_alimento VARCHAR(200) NOT NULL,
    marca VARCHAR(100),
    tipo_alimento VARCHAR(20) NOT NULL DEFAULT 'generico' CHECK (tipo_alimento IN ('generico', 'marca_comercial')),
    categoria VARCHAR(80),
    fonte_origem VARCHAR(25) NOT NULL DEFAULT 'fatsecret' CHECK (fonte_origem IN ('fatsecret', 'portugues_tradicional', 'utilizador_custom')),
    criado_por_utilizador_id UUID REFERENCES utilizadores(id) ON DELETE SET NULL,
    verificado_por_nutricionista BOOLEAN NOT NULL DEFAULT TRUE,
    criado_em TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_alimentos_nome ON alimentos (nome_alimento);
CREATE INDEX idx_alimentos_ean ON alimentos (codigo_barras_ean);

CREATE TABLE porcoes_alimento (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    alimento_id UUID NOT NULL REFERENCES alimentos(id) ON DELETE CASCADE,
    fatsecret_serving_id BIGINT,
    descricao_porcao VARCHAR(100) NOT NULL,
    quantidade_metrica DECIMAL(8,2),
    unidade_metrica VARCHAR(20),
    hidratos_carbono_g DECIMAL(6,2) NOT NULL CHECK (hidratos_carbono_g >= 0),
    fibra_alimentar_g DECIMAL(6,2) DEFAULT 0.00,
    acucares_g DECIMAL(6,2) DEFAULT 0.00,
    calorias_kcal DECIMAL(7,2) NOT NULL CHECK (calorias_kcal >= 0),
    proteinas_g DECIMAL(6,2) NOT NULL DEFAULT 0.00,
    gorduras_g DECIMAL(6,2) NOT NULL DEFAULT 0.00,
    gorduras_saturadas_g DECIMAL(6,2) DEFAULT 0.00,
    sodio_mg DECIMAL(7,2) DEFAULT 0.00,
    CONSTRAINT uq_alimento_porcao UNIQUE (alimento_id, descricao_porcao)
);

CREATE TABLE refeicoes_diario (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    paciente_id UUID NOT NULL REFERENCES pacientes(id) ON DELETE CASCADE,
    tipo_refeicao VARCHAR(25) NOT NULL CHECK (tipo_refeicao IN ('pequeno_almoco', 'almoco', 'lanche', 'jantar', 'snacks_outros')),
    data_refeicao DATE NOT NULL,
    hora_prevista TIME,
    estado_consumo VARCHAR(20) NOT NULL DEFAULT 'planeada' CHECK (estado_consumo IN ('planeada', 'consumida', 'cancelada')),
    data_hora_consumo TIMESTAMPTZ,
    total_hidratos_g DECIMAL(6,2) NOT NULL DEFAULT 0.00,
    total_calorias_kcal DECIMAL(7,2) NOT NULL DEFAULT 0.00,
    impacto_glicemia_pos_prandial_mg_dl INT,
    notas TEXT,
    criado_em TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX idx_refeicoes_paciente_data ON refeicoes_diario (paciente_id, data_refeicao DESC);

CREATE TABLE itens_refeicao (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    refeicao_id UUID NOT NULL REFERENCES refeicoes_diario(id) ON DELETE CASCADE,
    alimento_id UUID NOT NULL REFERENCES alimentos(id) ON DELETE RESTRICT,
    porcao_id UUID NOT NULL REFERENCES porcoes_alimento(id) ON DELETE RESTRICT,
    quantidade_porcoes DECIMAL(4,2) NOT NULL DEFAULT 1.00 CHECK (quantidade_porcoes > 0),
    hidratos_calculados_g DECIMAL(6,2) NOT NULL,
    calorias_calculadas_kcal DECIMAL(7,2) NOT NULL,
    consumido_efetivamente BOOLEAN NOT NULL DEFAULT TRUE
);

-- Chave estrangeira atrasada de insulina para refeição
ALTER TABLE registos_insulina
    ADD CONSTRAINT fk_insulina_refeicao
    FOREIGN KEY (refeicao_associada_id) REFERENCES refeicoes_diario(id) ON DELETE SET NULL;

-- ---------------------------------------------------------------------------
-- 5. MÓDULO: MOTOR DE INTELIGÊNCIA ARTIFICIAL IRIS & ALERTAS CLÍNICOS
-- ---------------------------------------------------------------------------
CREATE TABLE previsoes_glicemia (
    id BIGSERIAL PRIMARY KEY,
    paciente_id UUID NOT NULL REFERENCES pacientes(id) ON DELETE CASCADE,
    timestamp_geracao TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    horizonte_minutos INT NOT NULL DEFAULT 30 CHECK (horizonte_minutos > 0),
    valor_previsto_mg_dl INT NOT NULL,
    intervalo_confianca_min_mg_dl INT NOT NULL,
    intervalo_confianca_max_mg_dl INT NOT NULL,
    risco_previsto VARCHAR(30) NOT NULL DEFAULT 'nenhum' CHECK (risco_previsto IN ('nenhum', 'hipoglicemia_iminente', 'hiperglicemia_iminente')),
    confianca_modelo_percent DECIMAL(5,2) DEFAULT 95.00,
    versao_modelo_ia VARCHAR(50) NOT NULL DEFAULT 'Iris-Predict-v1.4'
);

CREATE TABLE alertas (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    paciente_id UUID NOT NULL REFERENCES pacientes(id) ON DELETE CASCADE,
    leitura_origem_id BIGINT REFERENCES leituras_glicemia(id) ON DELETE SET NULL,
    previsao_origem_id BIGINT REFERENCES previsoes_glicemia(id) ON DELETE SET NULL,
    tipo_alerta VARCHAR(35) NOT NULL CHECK (tipo_alerta IN ('risco_descida_previsto', 'risco_subida_previsto', 'valor_baixo_critico', 'valor_alto_critico', 'sem_ligacao_sensor', 'sensor_a_expirar')),
    nivel_severidade VARCHAR(20) NOT NULL CHECK (nivel_severidade IN ('plain_info', 'warn_atencao', 'risk_grave')),
    titulo VARCHAR(150) NOT NULL,
    mensagem TEXT NOT NULL,
    acao_sugerida_iris TEXT,
    data_hora_disparo TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    estado_reconhecimento VARCHAR(25) NOT NULL DEFAULT 'pendente' CHECK (estado_reconhecimento IN ('pendente', 'reconhecido', 'resolvido', 'ignorado')),
    reconhecido_em TIMESTAMPTZ,
    reconhecido_por_utilizador_id UUID REFERENCES utilizadores(id),
    notas_resolucao VARCHAR(255)
);

CREATE TABLE interacoes_iris_chat (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    paciente_id UUID NOT NULL REFERENCES pacientes(id) ON DELETE CASCADE,
    pergunta_utilizador TEXT NOT NULL,
    resposta_iris TEXT NOT NULL,
    snapshot_contexto_clinico_json JSONB,
    data_hora TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    avaliacao_utilizador VARCHAR(20) CHECK (avaliacao_utilizador IN ('util', 'pouco_util', 'incorreto'))
);

-- ---------------------------------------------------------------------------
-- 6. MÓDULO: TELEMEDICINA, CONSULTAS & COMUNICAÇÃO CLÍNICA
-- ---------------------------------------------------------------------------
CREATE TABLE profissionais_saude (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    utilizador_id UUID UNIQUE NOT NULL REFERENCES utilizadores(id) ON DELETE CASCADE,
    numero_ordem_cedula VARCHAR(50) UNIQUE NOT NULL,
    especialidade VARCHAR(60) NOT NULL CHECK (especialidade IN ('Endocrinologia', 'Medicina Geral e Familiar', 'Enfermagem — Diabetes', 'Nutrição Clínica')),
    instituicao_clinica VARCHAR(150) DEFAULT 'Equipa clínica Voltik',
    biografia TEXT,
    ativo BOOLEAN NOT NULL DEFAULT TRUE
);

CREATE TABLE atribuicoes_paciente_profissional (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    paciente_id UUID NOT NULL REFERENCES pacientes(id) ON DELETE CASCADE,
    profissional_id UUID NOT NULL REFERENCES profissionais_saude(id) ON DELETE CASCADE,
    data_inicio DATE NOT NULL DEFAULT CURRENT_DATE,
    data_fim DATE,
    estado VARCHAR(20) NOT NULL DEFAULT 'ativo' CHECK (estado IN ('ativo', 'concluido', 'transferido')),
    CONSTRAINT uq_paciente_profissional UNIQUE (paciente_id, profissional_id)
);

CREATE TABLE teleconsultas (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    paciente_id UUID NOT NULL REFERENCES pacientes(id) ON DELETE CASCADE,
    profissional_id UUID NOT NULL REFERENCES profissionais_saude(id) ON DELETE CASCADE,
    data_hora_marcada TIMESTAMPTZ NOT NULL,
    duracao_minutos INT NOT NULL DEFAULT 30,
    tipo_consulta VARCHAR(30) NOT NULL DEFAULT 'Teleconsulta' CHECK (tipo_consulta IN ('Teleconsulta', 'Revisao_Assincrona')),
    estado VARCHAR(20) NOT NULL DEFAULT 'agendada' CHECK (estado IN ('agendada', 'em_curso', 'concluida', 'cancelada', 'nao_compareceu')),
    sala_webrtc_token VARCHAR(100) UNIQUE,
    notas_clinicas_consulta TEXT,
    plano_terapeutico_ajustado TEXT,
    criado_em TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE conversas_chat (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    paciente_id UUID NOT NULL REFERENCES pacientes(id) ON DELETE CASCADE,
    tipo_canal VARCHAR(30) NOT NULL CHECK (tipo_canal IN ('paciente_medico', 'paciente_cuidador', 'cuidador_medico')),
    titulo_topico VARCHAR(100),
    criado_em TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE relatorios_clinicos (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    paciente_id UUID NOT NULL REFERENCES pacientes(id) ON DELETE CASCADE,
    periodo_tipo VARCHAR(20) NOT NULL CHECK (periodo_tipo IN ('Dia', 'Semana', 'Mes', 'Personalizado')),
    data_inicio DATE NOT NULL,
    data_fim DATE NOT NULL,
    tempo_no_intervalo_tir_percent DECIMAL(5,2) NOT NULL,
    tempo_abaixo_intervalo_tbr_percent DECIMAL(5,2) NOT NULL DEFAULT 0.00,
    tempo_acima_intervalo_tar_percent DECIMAL(5,2) NOT NULL DEFAULT 0.00,
    glicemia_media_mg_dl INT NOT NULL,
    desvio_padrao_mg_dl DECIMAL(5,2),
    coeficiente_variacao_cv_percent DECIMAL(5,2),
    estimativa_hba1c_gmi_percent DECIMAL(4,2),
    episodios_hipoglicemia_baixo INT NOT NULL DEFAULT 0,
    episodios_hiperglicemia_alto INT NOT NULL DEFAULT 0,
    analise_impacto_trabalho TEXT,
    resumo_gerado_iris TEXT,
    ficheiro_pdf_url VARCHAR(500),
    gerado_em TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE mensagens_chat (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    conversa_id UUID NOT NULL REFERENCES conversas_chat(id) ON DELETE CASCADE,
    remetente_utilizador_id UUID NOT NULL REFERENCES utilizadores(id) ON DELETE CASCADE,
    conteudo_texto TEXT NOT NULL,
    tipo_anexo VARCHAR(25) NOT NULL DEFAULT 'nenhum' CHECK (tipo_anexo IN ('nenhum', 'relatorio_clinico_pdf', 'grafico_glicemia', 'foto_refeicao')),
    anexo_relatorio_id UUID REFERENCES relatorios_clinicos(id) ON DELETE SET NULL,
    data_hora_envio TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    lida BOOLEAN NOT NULL DEFAULT FALSE,
    data_hora_leitura TIMESTAMPTZ
);

CREATE TABLE auditoria_acesso_dados (
    id BIGSERIAL PRIMARY KEY,
    utilizador_autor_id UUID REFERENCES utilizadores(id) ON DELETE SET NULL,
    paciente_alvo_id UUID REFERENCES pacientes(id) ON DELETE SET NULL,
    tipo_operacao VARCHAR(40) NOT NULL CHECK (tipo_operacao IN ('visualizacao_glicemia_tempo_real', 'exportacao_relatorio_pdf', 'alteracao_terapia', 'consulta_diario_alimentar', 'revogacao_cuidador', 'concessao_consentimento', 'revogacao_consentimento')),
    endereco_ip VARCHAR(45) NOT NULL,
    user_agent VARCHAR(255),
    data_hora TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- 7. MÓDULO: CONSENTIMENTOS (RGPD) & NOTIFICAÇÕES
-- ---------------------------------------------------------------------------
CREATE TABLE consentimentos (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    utilizador_id UUID NOT NULL REFERENCES utilizadores(id) ON DELETE CASCADE,
    tipo_consentimento VARCHAR(40) NOT NULL CHECK (tipo_consentimento IN ('termos_servico', 'tratamento_dados_saude', 'processamento_ia_externa', 'participacao_testes')),
    versao_documento VARCHAR(20) NOT NULL,
    aceite_em TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revogado_em TIMESTAMPTZ,
    CONSTRAINT uq_consentimento_versao UNIQUE (utilizador_id, tipo_consentimento, versao_documento)
);
CREATE TABLE tokens_notificacao (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    utilizador_id UUID NOT NULL REFERENCES utilizadores(id) ON DELETE CASCADE,
    plataforma VARCHAR(10) NOT NULL CHECK (plataforma IN ('android', 'ios', 'web')),
    token_push VARCHAR(500) UNIQUE NOT NULL, -- token FCM (Android/web) ou APNs (iOS)
    ativo BOOLEAN NOT NULL DEFAULT TRUE,
    atualizado_em TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
-- Chaves estrangeiras atrasadas (tabelas de destino criadas mais abaixo no script)
ALTER TABLE perfis_utilizador
    ADD CONSTRAINT fk_perfil_organizacao
    FOREIGN KEY (organizacao_id) REFERENCES organizacoes(id) ON DELETE CASCADE;
ALTER TABLE perfis_utilizador
    ADD CONSTRAINT ck_gestor_tem_organizacao
    CHECK ((tipo_perfil = 'gestor_empresa') = (organizacao_id IS NOT NULL));
ALTER TABLE subscricoes_b2c
    ADD CONSTRAINT fk_subscricao_paciente
    FOREIGN KEY (paciente_beneficiario_id) REFERENCES pacientes(id) ON DELETE CASCADE;
CREATE INDEX idx_leituras_sincronizacao ON leituras_glicemia (paciente_id, sincronizado_em DESC);

-- ---------------------------------------------------------------------------
-- 8. ÍNDICES NAS CHAVES ESTRANGEIRAS (o PostgreSQL não os cria automaticamente)
-- ---------------------------------------------------------------------------
CREATE INDEX idx_codigos_convite_empresa_organizacao_id ON codigos_convite_empresa (organizacao_id);
CREATE INDEX idx_codigos_convite_empresa_criado_por_utilizador_id ON codigos_convite_empresa (criado_por_utilizador_id);
CREATE INDEX idx_codigos_convite_empresa_utilizado_por_utilizador_id ON codigos_convite_empresa (utilizado_por_utilizador_id);
CREATE INDEX idx_subscricoes_b2c_utilizador_id ON subscricoes_b2c (utilizador_id);
CREATE INDEX idx_pacientes_organizacao_id ON pacientes (organizacao_id);
CREATE INDEX idx_contactos_emergencia_paciente_id ON contactos_emergencia (paciente_id);
CREATE INDEX idx_vinculos_cuidador_cuidador_utilizador_id ON vinculos_cuidador (cuidador_utilizador_id);
CREATE INDEX idx_dispositivos_paciente_id ON dispositivos (paciente_id);
CREATE INDEX idx_leituras_glicemia_dispositivo_id ON leituras_glicemia (dispositivo_id);
CREATE INDEX idx_registos_insulina_paciente_id ON registos_insulina (paciente_id);
CREATE INDEX idx_registos_atividade_fisica_paciente_id ON registos_atividade_fisica (paciente_id);
CREATE INDEX idx_registos_atividade_fisica_dispositivo_id ON registos_atividade_fisica (dispositivo_id);
CREATE INDEX idx_alimentos_criado_por_utilizador_id ON alimentos (criado_por_utilizador_id);
CREATE INDEX idx_itens_refeicao_refeicao_id ON itens_refeicao (refeicao_id);
CREATE INDEX idx_itens_refeicao_alimento_id ON itens_refeicao (alimento_id);
CREATE INDEX idx_itens_refeicao_porcao_id ON itens_refeicao (porcao_id);
CREATE INDEX idx_previsoes_glicemia_paciente_id ON previsoes_glicemia (paciente_id);
CREATE INDEX idx_alertas_leitura_origem_id ON alertas (leitura_origem_id);
CREATE INDEX idx_alertas_previsao_origem_id ON alertas (previsao_origem_id);
CREATE INDEX idx_alertas_reconhecido_por_utilizador_id ON alertas (reconhecido_por_utilizador_id);
CREATE INDEX idx_interacoes_iris_chat_paciente_id ON interacoes_iris_chat (paciente_id);
CREATE INDEX idx_atribuicoes_paciente_profissional_profissional_id ON atribuicoes_paciente_profissional (profissional_id);
CREATE INDEX idx_teleconsultas_paciente_id ON teleconsultas (paciente_id);
CREATE INDEX idx_conversas_chat_paciente_id ON conversas_chat (paciente_id);
CREATE INDEX idx_relatorios_clinicos_paciente_id ON relatorios_clinicos (paciente_id);
CREATE INDEX idx_mensagens_chat_remetente_utilizador_id ON mensagens_chat (remetente_utilizador_id);
CREATE INDEX idx_mensagens_chat_anexo_relatorio_id ON mensagens_chat (anexo_relatorio_id);
CREATE INDEX idx_auditoria_acesso_dados_utilizador_autor_id ON auditoria_acesso_dados (utilizador_autor_id);
CREATE INDEX idx_auditoria_acesso_dados_paciente_alvo_id ON auditoria_acesso_dados (paciente_alvo_id);
CREATE INDEX idx_tokens_notificacao_utilizador_id ON tokens_notificacao (utilizador_id);
CREATE INDEX idx_registos_insulina_refeicao_associada_id ON registos_insulina (refeicao_associada_id);
CREATE INDEX idx_perfis_utilizador_organizacao_id ON perfis_utilizador (organizacao_id);
CREATE INDEX idx_subscricoes_b2c_paciente_beneficiario_id ON subscricoes_b2c (paciente_beneficiario_id);
CREATE INDEX idx_alertas_paciente_data ON alertas (paciente_id, data_hora_disparo DESC);
CREATE INDEX idx_mensagens_conversa_data ON mensagens_chat (conversa_id, data_hora_envio);
CREATE INDEX idx_teleconsultas_profissional_data ON teleconsultas (profissional_id, data_hora_marcada);

-- ---------------------------------------------------------------------------
-- 9. AUDITORIA SÓ DE INSERÇÃO: a API pode registar acessos, mas nunca alterá-los ou apagá-los
-- ---------------------------------------------------------------------------
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'voltik_api') THEN
        REVOKE UPDATE, DELETE ON auditoria_acesso_dados FROM voltik_api;
    END IF;
END
$$;
