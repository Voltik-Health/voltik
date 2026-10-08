// Package config lê a configuração da API a partir de variáveis de ambiente.
// Os valores reais vêm do .env de cada ambiente no servidor, nunca do Git.
package config

import (
	"os"
	"strings"
)

type Config struct {
	Ambiente    string          // local | staging | producao
	Porta       string          // porta HTTP dentro do contentor
	BaseDados   string          // URL de ligação, com o papel voltik_api
	OrigensCORS map[string]bool // origens de frontend autorizadas
}

func Carregar() Config {
	origens := map[string]bool{}
	for _, o := range strings.Split(os.Getenv("CORS_ORIGENS"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			origens[o] = true
		}
	}
	return Config{
		Ambiente:    valorOu("AMBIENTE", "local"),
		Porta:       valorOu("PORTA", "8080"),
		BaseDados:   os.Getenv("DATABASE_URL"),
		OrigensCORS: origens,
	}
}

func valorOu(chave, omissao string) string {
	if v := os.Getenv(chave); v != "" {
		return v
	}
	return omissao
}
