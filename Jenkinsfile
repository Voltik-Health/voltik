// =============================================================================
// VOLTIK · pipeline (Multibranch Pipeline no Jenkins)
//   qualquer ramo / PR -> testes (dentro do docker build do backend)
//   ramo staging       -> publica automaticamente em staging
//   ramo main          -> pede confirmação e publica em produção
// =============================================================================
pipeline {
  agent any
  options {
    disableConcurrentBuilds()
    timestamps()
  }
  stages {
    stage('Testes e imagem') {
      steps {
        sh '''
          VERSAO=$(git rev-parse --short=12 HEAD)
          docker build --build-arg VERSAO=$VERSAO -t voltik-api:$VERSAO backend
        '''
      }
    }
    stage('Publicar em staging') {
      when { branch 'staging' }
      steps {
        sh 'infra/server/publicar.sh staging $(git rev-parse --short=12 HEAD)'
      }
    }
    stage('Confirmar produção') {
      when { branch 'main' }
      steps {
        input message: 'Publicar esta versão em PRODUÇÃO?', ok: 'Publicar'
      }
    }
    stage('Publicar em produção') {
      when { branch 'main' }
      steps {
        sh 'infra/server/publicar.sh producao $(git rev-parse --short=12 HEAD)'
      }
    }
  }
  post {
    always {
      sh 'docker image prune -f --filter "until=168h" || true'
    }
  }
}
