// =============================================================================
// VOLTIK · pipeline (Jenkins Multibranch Pipeline)
//   any branch / PR  -> tests (run inside the backend docker build)
//   staging branch   -> deploys to staging automatically
//   main branch      -> asks for confirmation, then deploys to production
// =============================================================================
pipeline {
  agent any
  options {
    disableConcurrentBuilds()
    timestamps()
  }
  stages {
    stage('Test and build image') {
      steps {
        sh '''
          VERSION=$(git rev-parse --short=12 HEAD)
          docker build --build-arg VERSION=$VERSION -t voltik-api:$VERSION backend
        '''
      }
    }
    stage('Deploy to staging') {
      when { branch 'staging' }
      steps {
        sh 'infra/server/deploy.sh staging $(git rev-parse --short=12 HEAD)'
      }
    }
    stage('Confirm production') {
      when { branch 'main' }
      steps {
        input message: 'Deploy this version to PRODUCTION?', ok: 'Deploy'
      }
    }
    stage('Deploy to production') {
      when { branch 'main' }
      steps {
        sh 'infra/server/deploy.sh production $(git rev-parse --short=12 HEAD)'
      }
    }
  }
  post {
    always {
      sh 'docker image prune -f --filter "until=168h" || true'
    }
  }
}
