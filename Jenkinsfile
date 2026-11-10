// ==============================================================================
// VOLTIK · Modular Monorepo CI/CD Pipeline (Jenkinsfile)
// Architecture: Config-Driven (deploy_config.json) + Path-Based Change Detection
// ==============================================================================

def currentStage = 'Initialization'
def config = [:]
def targetEnv = 'staging'

// Service execution flags dynamically resolved via git change detection
def changedApi = false
def changedDb = false
def changedWeb = false
def changedML = false
def changedAndroid = false
def changedIOS = false
def isFullBuild = false

pipeline {
    agent any

    options {
        disableConcurrentBuilds()
        timeout(time: 30, unit: 'MINUTES')
    }

    environment {
        BRANCH_NAME = "${env.BRANCH_NAME}"
        INFRA_DIR = "voltik-infra"
    }

    stages {
        // ----------------------------------------------------------------------
        // STAGE 1: PREPARATION & CHANGE DETECTION
        // ----------------------------------------------------------------------
        stage('Stage 1: Preparation & Change Detection') {
            steps {
                script {
                    currentStage = env.STAGE_NAME
                    echo "🔍 Inspecting target branch and git change set..."

                    if (env.BRANCH_NAME == 'main') {
                        targetEnv = 'production'
                    } else {
                        targetEnv = 'staging'
                    }
                    echo "🎯 Target Environment: ${targetEnv.toUpperCase()}"

                    dir(env.INFRA_DIR) {
                        git branch: 'main', credentialsId: 'github-credentials', url: 'https://github.com/Voltik-Health/voltik-infra.git'
                    }

                    config = readJSON file: "${env.INFRA_DIR}/Jenkins/deploy_config.json"
                    echo "📋 Configuration loaded: Project ${config.project} v${config.version}"

                    def commitMsg = sh(script: 'git log -1 --pretty=%B', returnStdout: true).trim()
                    if (commitMsg.contains('[FULL_BUILD]') || env.BRANCH_NAME == 'main') {
                        isFullBuild = true
                        echo "🚀 FULL BUILD ENABLED: All modules will be validated."
                    }

                    def diff = sh(script: 'git diff --name-only HEAD~1 HEAD 2>/dev/null || git status --porcelain', returnStdout: true)
                    echo "📝 Detected changes:\n${diff}"

                    if (isFullBuild || diff.contains('backend/') || diff.contains('api/')) {
                        changedApi = true
                        echo "  ✔ Module API (Go): ACTIVATED"
                    }
                    if (isFullBuild || diff.contains('database/')) {
                        changedDb = true
                        echo "  ✔ Module Database: ACTIVATED"
                    }
                    if (isFullBuild || diff.contains('apps/web/')) {
                        changedWeb = true
                        echo "  ✔ Module Web (React): ACTIVATED"
                    }
                    if (isFullBuild || diff.contains('ml/')) {
                        changedML = true
                        echo "  ✔ Module ML (FastAPI / Iris): ACTIVATED"
                    }
                    if (isFullBuild || diff.contains('apps/android/')) {
                        changedAndroid = true
                        echo "  ✔ Module Android: ACTIVATED"
                    }
                    if (isFullBuild || diff.contains('apps/ios/')) {
                        changedIOS = true
                        echo "  ✔ Module iOS: ACTIVATED"
                    }
                }
            }
        }

        // ----------------------------------------------------------------------
        // STAGE 2: TESTS & QUALITY GATES (Parallel execution for changed modules)
        // ----------------------------------------------------------------------
        stage('Stage 2: Tests & Quality Gates') {
            parallel {
                stage('Test: Database') {
                    when { expression { return changedDb && config.modules.database.enabled } }
                    steps {
                        script {
                            currentStage = env.STAGE_NAME
                            echo "🔍 Validating SQL schema migrations..."
                            sh config.modules.database.validate_cmd
                        }
                    }
                }

                stage('Test: Backend Go API') {
                    when { expression { return changedApi && config.modules.backend_api.enabled } }
                    steps {
                        script {
                            currentStage = env.STAGE_NAME
                            echo "🧪 Running Go vet and unit tests..."
                            dir(config.modules.backend_api.dir) {
                                sh "go vet ./..."
                                sh config.modules.backend_api.test_cmd
                            }
                        }
                    }
                }

                stage('Test: Web Portal') {
                    when { expression { return changedWeb && config.modules.web.enabled } }
                    steps {
                        script {
                            currentStage = env.STAGE_NAME
                            echo "🧪 Running Web portal tests..."
                            dir(config.modules.web.dir) {
                                sh config.modules.web.install_cmd
                                sh config.modules.web.test_cmd
                            }
                        }
                    }
                }

                stage('Test: ML Service') {
                    when { expression { return changedML && config.modules.ml_service.enabled } }
                    steps {
                        script {
                            currentStage = env.STAGE_NAME
                            echo "🧪 Running Python ML tests..."
                            dir(config.modules.ml_service.dir) {
                                sh config.modules.ml_service.test_cmd
                            }
                        }
                    }
                }
            }
        }

        // ----------------------------------------------------------------------
        // STAGE 3: CONTAINER IMAGE BUILD (Docker)
        // ----------------------------------------------------------------------
        stage('Stage 3: Build Container Images') {
            when {
                allOf {
                    expression { return env.CHANGE_ID == null }
                    anyOf {
                        branch 'staging'
                        branch 'main'
                    }
                    expression { return (changedApi || changedWeb || changedML) }
                }
            }
            steps {
                script {
                    currentStage = env.STAGE_NAME
                    def imageTag = (targetEnv == 'production') ? 'prod-blue' : 'staging'

                    if (changedApi && config.modules.backend_api.enabled) {
                        echo "🐳 Building Backend API image (voltik-api:${imageTag})..."
                        sh "docker build -t voltik-api:${imageTag} -f backend/Dockerfile backend"
                    }
                    if (changedWeb && config.modules.web.enabled) {
                        echo "🐳 Building Web Portal image (voltik-web:${imageTag})..."
                        sh "docker build -t voltik-web:${imageTag} -f apps/web/Dockerfile apps/web"
                    }
                    if (changedML && config.modules.ml_service.enabled) {
                        def mlTag = (targetEnv == 'production') ? 'prod' : 'staging'
                        echo "🐳 Building ML Service image (voltik-ml:${mlTag})..."
                        sh "docker build -t voltik-ml:${mlTag} -f ml/Dockerfile ml"
                    }
                }
            }
        }

        // ----------------------------------------------------------------------
        // STAGE 4: SERVER DEPLOYMENT & VERIFICATION
        // ----------------------------------------------------------------------
        stage('Stage 4: Server Deployment') {
            when {
                allOf {
                    expression { return env.CHANGE_ID == null }
                    anyOf {
                        branch 'staging'
                        branch 'main'
                    }
                }
            }
            steps {
                script {
                    currentStage = env.STAGE_NAME

                    if (changedDb && config.modules.database.enabled) {
                        echo "🚀 Applying database migrations (${targetEnv})..."
                        sh "bash ${env.INFRA_DIR}/Jenkins/scripts/deploy.sh ${targetEnv} db"
                    }

                    if (changedApi && config.modules.backend_api.enabled) {
                        echo "🚀 Deploying Backend API service (${targetEnv})..."
                        sh "bash ${env.INFRA_DIR}/Jenkins/scripts/deploy.sh ${targetEnv} api"
                    }

                    if (changedWeb && config.modules.web.enabled) {
                        echo "🚀 Deploying Web Portal service (${targetEnv})..."
                        sh "bash ${env.INFRA_DIR}/Jenkins/scripts/deploy.sh ${targetEnv} web"
                    }

                    if (changedML && config.modules.ml_service.enabled) {
                        echo "🚀 Deploying ML Service (${targetEnv})..."
                        sh "bash ${env.INFRA_DIR}/Jenkins/scripts/deploy.sh ${targetEnv} ml"
                    }
                }
            }
        }
    }

    // --------------------------------------------------------------------------
    // POST-BUILD ACTIONS: NOTIFICATIONS & WORKSPACE CLEANUP
    // --------------------------------------------------------------------------
    post {
        success {
            echo "✅ SUCCESS: Voltik pipeline completed successfully for [${targetEnv.toUpperCase()}]!"
            script {
                sh "bash ${env.INFRA_DIR}/Jenkins/scripts/notify-slack.sh 'SUCCESS' 'Deployment completed for ${targetEnv}' || true"
            }
            cleanWs()
        }
        failure {
            echo "❌ FAILURE: Pipeline failed at stage: ${currentStage}!"
            script {
                sh "bash ${env.INFRA_DIR}/Jenkins/scripts/notify-slack.sh 'FAILURE' '${currentStage}' || true"
            }
            cleanWs()
        }
    }
}
